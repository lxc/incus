package drivers

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

// Cmd represents a running command for an Qemu VM.
type qemuCmd struct {
	attachedChildPid int
	cmd              incus.Operation
	dataDone         chan bool
	controlSendCh    chan api.InstanceExecControl
	controlResCh     chan error
	cleanupFunc      func()
}

// PID returns the attached child's process ID.
func (c *qemuCmd) PID() int {
	return c.attachedChildPid
}

// Signal sends a signal to the command.
func (c *qemuCmd) Signal(sig unix.Signal) error {
	command := api.InstanceExecControl{
		Command: "signal",
		Signal:  int(sig),
	}

	// Check handler hasn't finished.
	select {
	case <-c.dataDone:
		return fmt.Errorf("no such process") // Aligns with error retured from unix.Kill in lxc's Signal().
	default:
	}

	c.controlSendCh <- command
	err := <-c.controlResCh
	if err != nil {
		return err
	}

	logger.Debugf(`Forwarded signal "%d" to the agent`, sig)
	return nil
}

// Wait for the command to end and returns its exit code and any error.
func (c *qemuCmd) Wait() (int, error) {
	err := c.cmd.Wait()

	exitStatus := -1
	opAPI := c.cmd.Get()
	if opAPI.Metadata != nil {
		exitStatusRaw, ok := opAPI.Metadata["return"].(float64)
		if ok {
			exitStatus = int(exitStatusRaw)

			// Convert special exit statuses into errors.
			switch exitStatus {
			case 127:
				err = ErrExecCommandNotFound
			case 126:
				err = ErrExecCommandNotExecutable
			}
		}
	}

	if err != nil {
		// Error of type EOF indicates the session ended unexpectedly,
		// so we inform the client of the disconnection with a more
		// descriptive message.
		if errors.Is(err, io.EOF) {
			return exitStatus, fmt.Errorf("Disconnected")
		}

		return exitStatus, err
	}

	<-c.dataDone

	if c.cleanupFunc != nil {
		defer c.cleanupFunc()
	}

	return exitStatus, nil
}

// WindowResize resizes the running command's window.
func (c *qemuCmd) WindowResize(fd, winchWidth, winchHeight int) error {
	command := api.InstanceExecControl{
		Command: "window-resize",
		Args: map[string]string{
			"width":  strconv.Itoa(winchWidth),
			"height": strconv.Itoa(winchHeight),
		},
	}

	// Check handler hasn't finished.
	select {
	case <-c.dataDone:
		return fmt.Errorf("no such process") // Aligns with error retured from unix.Kill in lxc's Signal().
	default:
	}

	c.controlSendCh <- command
	err := <-c.controlResCh
	if err != nil {
		return err
	}

	logger.Debugf(`Forwarded window resize "%dx%d" to the agent`, winchWidth, winchHeight)
	return nil
}
