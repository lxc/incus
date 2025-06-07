package qmp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func tQmpLogSetup(t *testing.T) *qmpLog {
	t.Helper()
	logFile := filepath.Join(t.TempDir(), t.Name()+"_qmp.log")
	qlog, err := newQmpLog(logFile)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := os.RemoveAll(logFile)
		if err != nil {
			t.Fatal(err)
		}
	})

	return qlog
}

func TestNewQmpLog(t *testing.T) {
	qlog := tQmpLogSetup(t)
	err := qlog.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestQmpLogWrite(t *testing.T) {
	qlog := tQmpLogSetup(t)
	command := `{"execute":"cont","id":26}`
	reply := `{"return": {}, "id": 26}`
	_, err := fmt.Fprintf(qlog, "[%s] QUERY: %s\n",
		time.Now().Format(time.RFC3339), command)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fmt.Fprintf(qlog, "[%s] REPLY: %s\n\n",
		time.Now().Format(time.RFC3339), reply)
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(qlog.logFile)
	if err != nil {
		t.Fatal(err)
	}

	s := string(b)
	if !strings.Contains(s, command) || !strings.Contains(s, reply) {
		t.Fatal(s)
	}

	err = qlog.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestQmpLogClose(t *testing.T) {
	qlog := tQmpLogSetup(t)
	eg := errgroup.Group{}
	command := `{"execute":"cont","id":26}`
	reply := `{"return": {}, "id": 26}`
	event := `{"event":"STOP"}`
	// simulate run command logging
	eg.Go(func() error {
		_, err := fmt.Fprintf(qlog, "[%s] QUERY: %s\n",
			time.Now().Format(time.RFC3339), command)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(qlog, "[%s] REPLY: %s\n\n",
			time.Now().Format(time.RFC3339), reply)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		for range 10 {
			_, err := fmt.Fprintf(qlog, "[%s] EVENT: %s\n\n",
				time.Now().Format(time.RFC3339), event)
			if err != nil {
				return err
			}
		}

		return nil
	})

	err := eg.Wait()
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(qlog.logFile)
	if err != nil {
		t.Fatal(err)
	}

	s := string(b)
	if !strings.Contains(s, command) ||
		!strings.Contains(s, reply) ||
		!strings.Contains(s, event) {
		t.Fatal(s)
	}

	err = qlog.Close()
	if err != nil {
		t.Fatal(err)
	}
}
