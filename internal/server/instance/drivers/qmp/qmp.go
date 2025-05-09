package qmp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// qmpDisconnect closes the QEMU monitor socket connection.
func (m *Monitor) qmpDisconnect() error {
	err := m.c.Close()
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}

	return err
}

// qmpConnect sets up a QEMU QMP connection.
func (m *Monitor) qmpConnect() error {
	enc := json.NewEncoder(m.c)
	dec := json.NewDecoder(m.c)

	// Check for banner on startup
	ban := struct {
		QMP struct {
			Capabilities []string `json:"capabilities"`
		} `json:"QMP"`
	}{}

	err := dec.Decode(&ban)
	if err != nil {
		return err
	}

	m.oobSupported = slices.Contains(ban.QMP.Capabilities, "oob")

	// Issue capabilities handshake
	id := m.qmpIncreaseID()
	cmd := Command{Execute: "qmp_capabilities", ID: id}
	err = enc.Encode(cmd)
	if err != nil {
		return err
	}

	// Check for no error on return
	r := &Response{}
	err = dec.Decode(r)
	if err != nil {
		return err
	}

	if r.Error != nil {
		err = fmt.Errorf("%s: %s", r.Error.Class, r.Error.Desc)
		return err
	}

	if r.ID != id {
		return fmt.Errorf("reply id %d and command id %d mismatch", r.ID, id)
	}

	// Make sure events dispatch non blocking
	events := make(chan Event, EventQueueLength)

	go m.qmpListen(m.c, events, &m.replies)

	m.events = events

	return nil
}

// qmpEvents streams QEMU QMP qmpEvents.
func (m *Monitor) qmpEvents(context.Context) (<-chan Event, error) {
	return m.events, nil
}

// qmpListen listen incoming socket connection for events and responses.
func (m *Monitor) qmpListen(r io.Reader, events chan<- Event, replies *sync.Map) {
	defer close(events)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		e := Event{}

		b := scanner.Bytes()
		err := json.Unmarshal(b, &e)
		if err != nil {
			logger.Errorf("failed to unmarshal event: %v", err)
			continue
		}

		if e.Event != "" {
			// Make sure events dispatch non blocking
			logf := m.qmpEventLog(&e)
			select {
			case events <- e:
				if logf != nil {
					_, err = logf("EVENT(dispatched): %s\n\n", b)
				}

			default:
				if logf != nil {
					_, err = logf("EVENT(discarded): %s\n\n", b)
				}
			}

			if err != nil {
				logger.Errorf("failed to log the event: %v", err)
			}

			continue
		}

		r := rawResponse{}
		err = json.Unmarshal(b, &r)
		if err != nil {
			logger.Errorf("failed to unmarshal raw response: %v", err)
			continue
		}

		key := r.ID
		if key == ZeroKey { // discard
			logger.Debugf("Discard unknown response: %s", b)
			continue
		}

		val, ok := replies.LoadAndDelete(key)
		if !ok { // discard
			logger.Debug("no need reply, discard")
			continue
		}

		reply, ok := val.(chan rawResponse)
		if !ok { // discard
			logger.Errorf("failed to cast chan rawResponse")
			continue
		}

		// copy raw byte slice to avoid the weird QEMU QMP bug
		r.b = make([]byte, len(b))
		copy(r.b, b)

		reply <- r
	}

	err := scanner.Err()
	if err != nil {
		r := rawResponse{
			err: err,
		}

		errReply := make(chan rawResponse, 1)
		replies.Store(ZeroKey, errReply)
		errReply <- r
	}
}

// qmpRun run qmp command with nil file.
func (m *Monitor) qmpRun(req *Command, rep any) error {
	// Just call RunWithFile with no file
	return m.qmpRunWithFile(req, nil, rep)
}

// qmpIncreaseID increase ID and skip zero.
func (m *Monitor) qmpIncreaseID() uint32 {
	id := m.id.Add(1)
	if id == ZeroKey {
		id = m.id.Add(1)
	}

	return id
}

func (m *Monitor) qmpWriteMsg(b []byte, file *os.File) error {
	if file == nil {
		// Just send a normal command through.
		_, err := m.c.Write(b)
		if err != nil {
			return err
		}
	} else {
		unixConn, ok := m.c.(*net.UnixConn)
		if !ok {
			return fmt.Errorf("RunWithFile only works with unix monitor sockets")
		}

		if !m.oobSupported {
			return fmt.Errorf("The QEMU server doesn't support oob (needed for RunWithFile)")
		}

		// Send the command along with the file descriptor.
		oob := unix.UnixRights(int(file.Fd()))
		_, _, err := unixConn.WriteMsgUnix(b, oob, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Monitor) qmpLogInit(logPath string) error {
	if m.logFile == nil && logPath != "" {
		var logfile *os.File
		logfile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}

		m.logFile = logfile
	}

	return nil
}

func (m *Monitor) qmpQueryLog(req *Command) func(string, ...any) (int, error) {
	if m.logFile == nil {
		return nil
	}

	excludedCommands := []string{"ringbuf-read"}
	logok := req.logok
	if !logok {
		logok = !slices.Contains(excludedCommands, req.Execute)
	}

	if logok {
		logf := func(format string, a ...any) (int, error) {
			m.logFileMu.Lock()
			defer m.logFileMu.Unlock()
			format = "[%s] " + format
			a = append([]any{time.Now().Format(time.RFC3339)}, a...)
			return fmt.Fprintf(m.logFile, format, a...)
		}

		return logf
	}

	return nil
}

func (m *Monitor) qmpEventLog(e *Event) func(string, ...any) (int, error) {
	if m.logFile != nil &&
		e != nil && e.Event != "" &&
		util.IsTrue(os.Getenv("INCUS_QMP_EVENTS_DEBUG")) {
		return func(format string, a ...any) (int, error) {
			m.logFileMu.Lock()
			defer m.logFileMu.Unlock()
			format = "[%s] " + format
			a = append([]any{time.Now().Format(time.RFC3339)}, a...)
			return fmt.Fprintf(m.logFile, format, a...)
		}
	}
	return nil
}

// qmpRunWithFile behaves like qmpRun but allows for passing a file through
// out-of-band data.
func (m *Monitor) qmpRunWithFile(req *Command, file *os.File, rep any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.qmpIncreaseID()
	req.ID = id
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}

	logf := m.qmpQueryLog(req)
	if logf != nil {
		_, err = logf("QUERY: %s\n", b)
		if err != nil {
			return err
		}
	}

	repCh := make(chan rawResponse, 1)
	m.replies.Store(id, repCh)
	err = m.qmpWriteMsg(b, file)
	if err != nil {
		m.replies.Delete(id)
		return err
	}

	// Wait for a response or error to our command
	r := <-repCh

	if r.err != nil {
		return r.err
	}

	if logf != nil {
		_, err = logf("REPLY: %s\n\n", r.b)
		if err != nil {
			return err
		}
	}

	if rep == nil { // Skip response parsing
		return nil
	}

	err = json.Unmarshal(r.b, rep)
	if err != nil {
		return err
	}

	return nil
}
