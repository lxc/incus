package qmp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v7/shared/logger"
)

type qemuMachineProtocol struct {
	oobSupported bool            // Out of band support or not
	uc           *net.UnixConn   // Underlying unix socket connection
	commandLock  chan struct{}   // Serialize running command (bounded acquisition)
	replies      sync.Map        // Replies channels
	events       <-chan qmpEvent // Events channel
	listeners    atomic.Uint32   // Listeners number
	cid          atomic.Uint32   // Auto increase command id
	log          *qmpLog         // qmp log
}

// qmpEvent represents a QEMU QMP event.
type qmpEvent struct {
	// Event name, e.g., BLOCK_JOB_COMPLETE
	Event string `json:"event"`

	// Arbitrary event data
	Data map[string]any `json:"data"`

	// Event timestamp, provided by QEMU.
	Timestamp *struct {
		Seconds      int64 `json:"seconds"`
		Microseconds int64 `json:"microseconds"`
	} `json:"timestamp"`
}

// qmpCommand represents a QMP command.
type qmpCommand struct {
	// Name of the command to run
	Execute string `json:"execute,omitempty"`

	// Name of the Out-off-band execution to run
	ExecuteOutOfBand string `json:"exec-oob,omitempty"`

	// Optional arguments for the above command.
	Arguments any `json:"arguments,omitempty"`

	// Optional id for transaction identification associated with the command
	// execution
	//
	// According QMP spec it should be any json value type. For incus `uint32`
	// (skip zero) is good enough to identify transaction.
	ID uint32 `json:"id,omitempty"`
}

// qmpResponse represents a QMP response with id and return.
type qmpResponse struct {
	// Optional id for transaction identification associated with the response
	ID uint32 `json:"id,omitempty"`

	// Return response return
	Return any `json:"return,omitempty"`
}

// qmpError represents a QMP response error.
type qmpError struct {
	Class string `json:"class,omitempty"`
	Desc  string `json:"desc,omitempty"`
}

func (e *qmpError) Error() string {
	if e == nil {
		return ""
	}

	return fmt.Sprintf("%s: %s", e.Class, e.Desc)
}

// rawResponse represents QMP raw response with id, error and raw bytes.
type rawResponse struct {
	// Optional id for transaction identification associated with the response
	ID uint32 `json:"id"`

	// Error response error
	Error *qmpError `json:"error,omitempty"`

	raw []byte // raw data, json field ignored
	err error  // runtime error, json field ignored
}

// disconnect closes the QEMU monitor socket connection.
func (qmp *qemuMachineProtocol) disconnect() error {
	qmp.listeners.Store(0)
	if qmp.log != nil {
		err := qmp.log.Close()
		if err != nil {
			return err
		}

		qmp.log = nil
	}

	return qmp.uc.Close()
}

// qmpIncreaseID increase ID and skip zero.
func (qmp *qemuMachineProtocol) qmpIncreaseID() uint32 {
	id := qmp.cid.Add(1)
	if id == 0 {
		id = qmp.cid.Add(1)
	}

	return id
}

// connect sets up a QMP connection.
func (qmp *qemuMachineProtocol) connect() error {
	qmp.commandLock = make(chan struct{}, 1)

	enc := json.NewEncoder(qmp.uc)
	dec := json.NewDecoder(qmp.uc)

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

	qmp.oobSupported = slices.Contains(ban.QMP.Capabilities, "oob")

	// Issue capabilities handshake
	id := qmp.qmpIncreaseID()
	cmd := qmpCommand{Execute: "qmp_capabilities", ID: id}
	err = enc.Encode(cmd)
	if err != nil {
		return err
	}

	// Check for no error on return
	r := &rawResponse{}
	err = dec.Decode(r)
	if err != nil {
		return err
	}

	if r.Error != nil {
		return r.Error
	}

	if r.ID != id {
		return fmt.Errorf("reply id %d and command id %d mismatch", r.ID, id)
	}

	// Initialize listener for command responses and asynchronous events.
	events := make(chan qmpEvent, 128)
	go qmp.listen(qmp.uc, events, &qmp.replies)
	qmp.events = events
	return nil
}

// getEvents streams QEMU QMP Events.
func (qmp *qemuMachineProtocol) getEvents(context.Context) (<-chan qmpEvent, error) {
	qmp.listeners.Add(1)
	return qmp.events, nil
}

func (qmp *qemuMachineProtocol) listen(r io.Reader, events chan<- qmpEvent, replies *sync.Map) {
	defer close(events)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var e qmpEvent

		b := scanner.Bytes()
		err := json.Unmarshal(b, &e)
		if err != nil {
			continue
		}

		// If data does not have an event type, it must be in response to a command.
		if e.Event == "" {
			r := rawResponse{}
			err = json.Unmarshal(b, &r)
			if err != nil {
				continue
			}

			key := r.ID
			if key == 0 {
				// Discard response without a request ID.
				continue
			}

			val, ok := replies.LoadAndDelete(key)
			if !ok {
				// Discard unexpected response.
				continue
			}

			reply, ok := val.(chan rawResponse)
			if !ok {
				// Skip bad messages.
				logger.Error("Failed to cast QMP reply to chan rawResponse")
				continue
			}

			r.raw = make([]byte, len(b))
			copy(r.raw, b)
			reply <- r

			continue
		}

		if qmp.log != nil && !slices.Contains([]string{"VSERPORT_CHANGE"}, e.Event) {
			_, err := fmt.Fprintf(qmp.log, "[%s] Event: %s\n", time.Now().Format(time.RFC3339), b)
			if err != nil {
				logger.Debugf("Failed to log event: %v", err)
			}
		}

		// If nobody is listening for events, do not bother sending them.
		if qmp.listeners.Load() == 0 {
			continue
		}

		events <- e
	}

	err := scanner.Err()
	if err == nil {
		err = errors.New("Monitor has exited")
	} else {
		logger.Warn("QMP monitor read failed", logger.Ctx{"err": err})
	}

	// Return the error to all existing requests.
	replies.Range(func(k any, v any) bool {
		reply, ok := v.(chan rawResponse)
		if !ok {
			// Skip bad messages.
			logger.Error("Failed to cast QMP reply to chan rawResponse")

			return true
		}

		reply <- rawResponse{err: err}

		return true
	})

	// Clear the map.
	replies.Clear()
}

// defaultCommandTimeout bounds queries so instance state rendering can't hang on a silent QEMU.
const defaultCommandTimeout = 2 * time.Second

// operationCommandTimeout is the hard cap for state-changing commands which can be legitimately slow.
const operationCommandTimeout = 2 * time.Minute

// commandTimeout holds the reply deadline and an optional slow-command warning threshold.
type commandTimeout struct {
	warn  time.Duration
	limit time.Duration
}

// Operational commands warn past their expected duration but only fail at the hard cap.
var (
	blockCommandTimeout = commandTimeout{warn: 5 * time.Second, limit: operationCommandTimeout}
	heavyCommandTimeout = commandTimeout{warn: 30 * time.Second, limit: operationCommandTimeout}
)

// commandTimeouts overrides defaultCommandTimeout for slow synchronous commands.
var commandTimeouts = map[string]commandTimeout{
	"block-commit":              blockCommandTimeout,
	"block-dirty-bitmap-remove": blockCommandTimeout,
	"block-export-add":          blockCommandTimeout,
	"block-job-cancel":          blockCommandTimeout,
	"block_resize":              blockCommandTimeout,
	"block_set_io_throttle":     blockCommandTimeout,
	"blockdev-mirror":           blockCommandTimeout,
	"blockdev-snapshot":         blockCommandTimeout,
	"change-backing-file":       blockCommandTimeout,
	"job-complete":              blockCommandTimeout,
	"job-dismiss":               blockCommandTimeout,
	"migrate":                   blockCommandTimeout,
	"migrate-continue":          blockCommandTimeout,
	"migrate-incoming":          blockCommandTimeout,
	"migrate-set-capabilities":  blockCommandTimeout,
	"migrate-set-parameters":    blockCommandTimeout,
	"migrate_cancel":            blockCommandTimeout,
	"nbd-server-start":          blockCommandTimeout,
	"nbd-server-stop":           blockCommandTimeout,
	"transaction":               blockCommandTimeout,
	"blockdev-add":              heavyCommandTimeout,
	"blockdev-del":              heavyCommandTimeout,
	"cont":                      heavyCommandTimeout,
	"device_add":                heavyCommandTimeout,
	"device_del":                heavyCommandTimeout,
	"netdev_add":                heavyCommandTimeout,
	"netdev_del":                heavyCommandTimeout,
	"object-add":                heavyCommandTimeout,
	"stop":                      heavyCommandTimeout,
	"system_reset":              heavyCommandTimeout,
	"query-block-jobs":          {limit: 5 * time.Second},
	"query-migrate":             {limit: 30 * time.Second},
	"screendump":                {limit: 10 * time.Second},
}

// commandName extracts the command name from a marshalled QMP request.
func commandName(command []byte) string {
	var req struct {
		Execute          string `json:"execute"`
		ExecuteOutOfBand string `json:"exec-oob"`
	}

	_ = json.Unmarshal(command, &req)
	if req.Execute != "" {
		return req.Execute
	}

	return req.ExecuteOutOfBand
}

// run executes the given QAPI command against a domain's QEMU instance.
func (qmp *qemuMachineProtocol) run(command []byte, id uint32) ([]byte, error) {
	// Just call RunWithFile with no file
	return qmp.runWithFile(command, nil, id)
}

func (qmp *qemuMachineProtocol) qmpWriteMsg(b []byte, file *os.File, timeout time.Duration) error {
	// A write only blocks once the socket buffer is full, meaning QEMU stopped
	// reading the monitor a long time ago. Time out rather than hold the command lock forever.
	err := qmp.uc.SetWriteDeadline(time.Now().Add(timeout))
	if err != nil {
		return err
	}

	if file == nil {
		// Just send a normal command through.
		_, err := qmp.uc.Write(b)
		return err
	}

	if !qmp.oobSupported {
		return errors.New("The QEMU server doesn't support oob (needed for RunWithFile)")
	}

	// Send the command along with the file descriptor.
	oob := unix.UnixRights(int(file.Fd()))
	_, _, err = qmp.uc.WriteMsgUnix(b, oob, nil)
	if err != nil {
		return err
	}

	return nil
}

// runWithFile executes for passing a file through out-of-band data.
func (qmp *qemuMachineProtocol) runWithFile(command []byte, file *os.File, id uint32) ([]byte, error) {
	if id == 0 {
		id = qmp.qmpIncreaseID()
		b, err := qmp.qmpInjectID(command, id)
		if err != nil {
			return nil, err
		}

		command = b
	}

	// Pick the timeout for this command.
	cmd := commandName(command)
	timeout, ok := commandTimeouts[cmd]
	if !ok {
		timeout = commandTimeout{limit: defaultCommandTimeout}
	}

	deadline := time.NewTimer(timeout.limit)
	defer deadline.Stop()

	// Only allow a single command in flight, giving up at the deadline so
	// short queries don't queue behind a slow command.
	select {
	case qmp.commandLock <- struct{}{}:
	case <-deadline.C:
		return nil, fmt.Errorf("%w: %q after %s", ErrMonitorBusy, cmd, timeout.limit)
	}

	defer func() { <-qmp.commandLock }()

	repCh := make(chan rawResponse, 1)
	qmp.replies.Store(id, repCh)

	err := qmp.qmpWriteMsg(command, file, timeout.limit)
	if err != nil {
		qmp.replies.Delete(id)
		return nil, err
	}

	// Warn once past the expected duration (warnCh stays nil, i.e. disabled, for queries).
	var warnCh <-chan time.Time
	if timeout.warn > 0 {
		warnTimer := time.NewTimer(timeout.warn)
		defer warnTimer.Stop()
		warnCh = warnTimer.C
	}

	// Wait for a response, error or timeout.
	started := time.Now()
	warned := false

	var res rawResponse

wait:
	for {
		select {
		case res = <-repCh:
			break wait
		case <-warnCh:
			warnCh = nil
			warned = true
			logger.Warn("QMP command taking longer than expected", logger.Ctx{"command": cmd, "expected": timeout.warn})
		case <-deadline.C:
			qmp.replies.Delete(id)
			return nil, fmt.Errorf("%w: %q after %s", ErrMonitorTimeout, cmd, timeout.limit)
		}
	}

	if warned {
		logger.Warn("Slow QMP command completed", logger.Ctx{"command": cmd, "duration": time.Since(started)})
	}

	if res.err != nil {
		return nil, res.err
	}

	if res.Error != nil {
		return nil, res.Error
	}

	return res.raw, nil
}

func (qmp *qemuMachineProtocol) qmpInjectID(command []byte, id uint32) ([]byte, error) {
	req := &qmpCommand{}
	err := json.Unmarshal(command, req)
	if err != nil {
		return nil, err
	}

	req.ID = id
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	return b, nil
}
