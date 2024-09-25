//go:build !windows

package subprocess

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSignalHandling(t *testing.T) {
	var a []string
	a = append(a, "testscript/signal.sh")
	var file *os.File
	p, err := NewProcess("sh", a, "testscript/signal_out.txt", "")

	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process ", err)
	}

	time.Sleep(2 * time.Second)
	err = p.Reload()
	if err != nil {
		t.Error("Unable to Reload process: ", err)
	}

	time.Sleep(2 * time.Second)
	err = p.Signal(10)
	if err != nil {
		t.Error("Unable to Signal process: ", err)
	}

	ecode, err := p.Wait(context.Background())
	if err == nil {
		t.Error("Did not exit with an error")
	} else if ecode != 1 {
		t.Error("Exit code is not 1: ", ecode)
	}

	file, err = os.OpenFile("testscript/signal_out.txt", os.O_RDWR, 0644)
	if err != nil {
		t.Error("Could not open file ", err)
	}

	defer func() { _ = file.Close() }()

	var text = make([]byte, 1024)
	for {
		_, err = file.Read(text)
		// Break if finally arrived at end of file
		if err == io.EOF {
			break
		}

		// Break if error occurred
		if err != nil && err != io.EOF {
			t.Error("Error in reading file ", err)
		}
	}

	if !strings.Contains(string(text), "Called with signal 1") {
		t.Errorf("Reload failed. File output mismatch. Got %s", string(text))
	}

	if !strings.Contains(string(text), "Called with signal 10") {
		t.Errorf("Signal failed. File output mismatch. Got %s", string(text))
	}

	err = os.Remove("testscript/signal_out.txt")
	if err != nil {
		t.Error("Could not delete file ", err)
	}
}

// tests newprocess, start, stop, save, import, restart, wait.
func TestStopRestart(t *testing.T) {
	var a []string
	a = append(a, "testscript/stoprestart.sh")

	p, err := NewProcess("sh", a, "", "")
	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	err = p.Stop()
	if err != nil {
		t.Error("Failed to stop process: ", err)
	}

	err = p.Save("testscript/test2.yaml")
	if err != nil {
		t.Error("Failed to save process: ", err)
	}

	p, err = ImportProcess("testscript/test2.yaml")
	if err != nil {
		t.Error("Failed to import process: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	err = p.Restart(context.Background())
	if err != nil {
		t.Error("Failed to restart process: ", err)
	}

	exitcode, err := p.Wait(context.Background())
	if err != nil {
		t.Error("Could not wait for process: ", err)
	} else if exitcode != 0 {
		t.Errorf("Exit code expected to be 0 but got %d", exitcode)
	}

	err = os.Remove("testscript/test2.yaml")
	if err != nil {
		t.Error("Could not delete file: ", err)
	}
}

func TestProcessStartWaitExit(t *testing.T) {
	var a []string
	var file *os.File
	var exp string
	var text []byte
	a = append(a, "testscript/exit1.sh")
	p, err := NewProcess("sh", a, "testscript/out.txt", "")
	if err != nil {
		t.Error("Failed process creation: ", err)
	}

	err = p.Start(context.Background())
	if err != nil {
		t.Error("Failed to start process: ", err)
	}

	ecode, err := p.Wait(context.Background())
	if err == nil {
		t.Error("Did not exit with an error")
	} else if ecode != 1 {
		t.Error("Exit code is not 1: ", ecode)
	}

	file, err = os.OpenFile("testscript/out.txt", os.O_RDWR, 0644)
	if err != nil {
		t.Error("Could not open file: ", err)
	}

	defer func() { _ = file.Close() }()

	exp = "hello again\nwaiting now\n"
	// Read file, line by line
	text = make([]byte, len(exp))
	for {
		_, err = file.Read(text)
		// Break if finally arrived at end of file
		if err == io.EOF {
			break
		}
		// Break if error occurred
		if err != nil && err != io.EOF {
			t.Error("Error reading file: ", err)
		}
	}

	if string(text) != exp {
		t.Errorf("File output mismatch Expected %s got %s", "hello again\nwaiting now\n", string(text))
	}

	// Cleanup
	err = os.Remove("testscript/out.txt")
	if err != nil {
		t.Error("Could not delete file: ", err)
	}
}
