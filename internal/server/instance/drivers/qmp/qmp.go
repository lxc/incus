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
	"sync/atomic"

	"golang.org/x/sys/unix"
)

type qemuMachineProtocal struct {
	oobSupported bool               // Out of band support or not
	c            net.Conn           // Underlying connection
	mu           sync.Mutex         // Serialize running command
	stream       <-chan rawResponse // Send command responses and errors
	events       <-chan qmpEvent    // Events channel
	listeners    atomic.Uint32      // Listeners number
	cid          atomic.Uint32      // Auto increase command id
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
func (qmp *qemuMachineProtocal) disconnect() error {
	qmp.listeners.Store(0)
	return qmp.c.Close()
}

// qmpIncreaseID increase ID and skip zero.
func (qmp *qemuMachineProtocal) qmpIncreaseID() uint32 {
	const ZeroKey = uint32(0)
	id := qmp.cid.Add(1)
	if id == ZeroKey {
		id = qmp.cid.Add(1)
	}

	return id
}

// connect sets up a QMP connection.
func (qmp *qemuMachineProtocal) connect() error {
	enc := json.NewEncoder(qmp.c)
	dec := json.NewDecoder(qmp.c)

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

	// Initialize listener for command responses and asynchronous events
	events := make(chan qmpEvent)
	stream := make(chan rawResponse)
	go qmp.listen(qmp.c, events, stream)

	qmp.events = events
	qmp.stream = stream

	return nil
}

// getEvents streams QEMU QMP Events.
func (qmp *qemuMachineProtocal) getEvents(context.Context) (<-chan qmpEvent, error) {
	qmp.listeners.Add(1)
	return qmp.events, nil
}

func (qmp *qemuMachineProtocal) listen(r io.Reader, events chan<- qmpEvent, stream chan<- rawResponse) {
	defer close(events)
	defer close(stream)

	scanner := bufio.NewScanner(r)
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

			r.raw = make([]byte, len(b))
			copy(r.raw, b)
			stream <- r
			continue
		}

		// If nobody is listening for events, do not bother sending them.
		if qmp.listeners.Load() == 0 {
			continue
		}

		events <- e
	}

	err := scanner.Err()
	if err != nil {
		stream <- rawResponse{err: err}
	}
}

// run executes the given QAPI command against a domain's QEMU instance.
func (qmp *qemuMachineProtocal) run(command []byte) ([]byte, error) {
	// Just call RunWithFile with no file
	return qmp.runWithFile(command, nil)
}

// runWithFile executes for passing a file through out-of-band data.
func (qmp *qemuMachineProtocal) runWithFile(command []byte, file *os.File) ([]byte, error) {
	// Only allow a single command to be run at a time to ensure that responses
	// to a command cannot be mixed with responses from another command
	qmp.mu.Lock()
	defer qmp.mu.Unlock()

	if file == nil {
		// Just send a normal command through.
		_, err := qmp.c.Write(command)
		if err != nil {
			return nil, err
		}
	} else {
		unixConn, ok := qmp.c.(*net.UnixConn)
		if !ok {
			return nil, fmt.Errorf("RunWithFile only works with unix monitor sockets")
		}

		if !qmp.oobSupported {
			return nil, fmt.Errorf("The QEMU server doesn't support oob (needed for RunWithFile)")
		}

		// Send the command along with the file descriptor.
		oob := unix.UnixRights(int(file.Fd()))
		_, _, err := unixConn.WriteMsgUnix(command, oob, nil)
		if err != nil {
			return nil, err
		}
	}

	// Wait for a response or error to our command
	res := <-qmp.stream
	if res.err != nil {
		return nil, res.err
	}

	if res.Error != nil {
		return nil, res.Error
	}

	return res.raw, nil
}
