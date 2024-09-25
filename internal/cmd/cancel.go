package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
)

// CancelableWait waits for an operation and cancel it on SIGINT/SIGTERM.
func CancelableWait(rawOp any, progress *ProgressRenderer) error {
	var op incus.Operation
	var rop incus.RemoteOperation

	// Check what type of operation we're dealing with
	switch v := rawOp.(type) {
	case incus.Operation:
		op = v
	case incus.RemoteOperation:
		rop = v
	default:
		return fmt.Errorf("Invalid operation type for CancelableWait")
	}

	// Signal handling
	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)

	// Operation handling
	chOperation := make(chan error)
	go func() {
		if op != nil {
			chOperation <- op.Wait()
		} else {
			chOperation <- rop.Wait()
		}

		close(chOperation)
	}()

	count := 0
	for {
		var err error

		select {
		case err := <-chOperation:
			return err
		case <-chSignal:
			if op != nil {
				err = op.Cancel()
			} else {
				err = rop.CancelTarget()
			}

			if err == nil {
				return fmt.Errorf(i18n.G("Remote operation canceled by user"))
			}

			count++

			if count == 3 {
				return fmt.Errorf(i18n.G("User signaled us three times, exiting. The remote operation will keep running"))
			}

			if progress != nil {
				progress.Warn(fmt.Sprintf(i18n.G("%v (interrupt two more times to force)"), err), time.Second*5)
			}
		}
	}
}
