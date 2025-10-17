package cmd

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/termios"
)

// ProgressRenderer tracks the progress information.
type ProgressRenderer struct {
	Format string
	Quiet  bool

	maxLength int
	wait      time.Time
	done      bool
	lock      sync.Mutex
	terminal  int
}

func (p *ProgressRenderer) truncate(msg string) string {
	width, _, err := termios.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return msg
	}

	newSize := len(msg)
	if width < newSize {
		return ""
	}

	return msg
}

// Done prints the final status and prevents any update.
func (p *ProgressRenderer) Done(msg string) {
	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Mark this renderer as done
	p.done = true

	// Handle quiet mode
	if p.Quiet {
		msg = ""
	}

	// Truncate msg to terminal length
	msg = p.truncate(msg)

	// If we're not printing a completion message and nothing was printed before just return
	if msg == "" && p.maxLength == 0 {
		return
	}

	// Print the new message
	if msg != "" {
		msg += "\n"
	}

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print("\r")
	fmt.Print(msg)
}

// Update changes the status message to the provided string.
func (p *ProgressRenderer) Update(status string) {
	// Wait if needed
	timeout := time.Until(p.wait)
	if timeout.Seconds() > 0 {
		time.Sleep(timeout)
	}

	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Handle quiet mode
	if p.Quiet {
		return
	}

	// Skip status updates when not dealing with a terminal
	if p.terminal == 0 {
		if !termios.IsTerminal(int(os.Stdout.Fd())) {
			p.terminal = -1
		}

		p.terminal = 1
	}

	if p.terminal != 1 {
		return
	}

	// Print the new message
	msg := "%s"
	if p.Format != "" {
		msg = p.Format
	}

	msg = fmt.Sprintf(msg, status)

	// Truncate msg to terminal length
	msg = "\r" + p.truncate(msg)

	// Don't print if empty and never printed
	if len(msg) == 1 && p.maxLength == 0 {
		return
	}

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print(msg)
}

// Warn shows a temporary message instead of the status.
func (p *ProgressRenderer) Warn(status string, timeout time.Duration) {
	// Acquire rendering lock
	p.lock.Lock()
	defer p.lock.Unlock()

	// Check if we're already done
	if p.done {
		return
	}

	// Render the new message
	p.wait = time.Now().Add(timeout)
	msg := status

	// Truncate msg to terminal length
	msg = "\r" + p.truncate(msg)

	// Don't print if empty and never printed
	if len(msg) == 1 && p.maxLength == 0 {
		return
	}

	if len(msg) > p.maxLength {
		p.maxLength = len(msg)
	} else {
		fmt.Printf("\r%s", strings.Repeat(" ", p.maxLength))
	}

	fmt.Print(msg)
}

// UpdateProgress is a helper to update the status using an iopgress instance.
func (p *ProgressRenderer) UpdateProgress(progress ioprogress.ProgressData) {
	p.Update(progress.Text)
}

// UpdateOp is a helper to update the status using a REST API operation.
func (p *ProgressRenderer) UpdateOp(op api.Operation) {
	if op.Metadata == nil {
		return
	}

	for key, value := range op.Metadata {
		if !strings.HasSuffix(key, "_progress") {
			continue
		}

		p.Update(value.(string))
		break
	}
}
