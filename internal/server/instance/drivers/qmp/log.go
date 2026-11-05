package qmp

import (
	"fmt"
	"os"
	"sync"
)

// qmpLogMaxEntry bounds a single entry, commands carry client-chosen names.
const qmpLogMaxEntry = 64 * 1024

// qmpLogMaxSize is the size at which the log is rotated.
const qmpLogMaxSize = 16 * 1024 * 1024

type qmpLog struct {
	logFile string
	log     *os.File
	mu      sync.Mutex
}

func newQmpLog(logFile string) (*qmpLog, error) {
	if logFile == "" {
		return nil, fmt.Errorf("Log file path is empty")
	}

	ql := &qmpLog{
		logFile: logFile,
	}

	err := ql.open()
	if err != nil {
		return nil, err
	}

	return ql, nil
}

func (ql *qmpLog) open() error {
	if ql.log == nil {
		ql.mu.Lock()
		defer ql.mu.Unlock()
		log, err := os.OpenFile(ql.logFile,
			os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}

		ql.log = log
	}

	return nil
}

// Write writes len(b) bytes from b to the channel.
func (ql *qmpLog) Write(p []byte) (n int, err error) {
	if ql == nil || ql.log == nil {
		return 0, nil
	}

	ql.mu.Lock()
	defer ql.mu.Unlock()

	// Ignore writes after close.
	if ql.log == nil {
		return 0, nil
	}

	if len(p) > qmpLogMaxEntry {
		p = append(p[:qmpLogMaxEntry:qmpLogMaxEntry], []byte("... (truncated)\n")...)
	}

	// Rotate once the file would exceed the limit, it's otherwise only rotated at VM start.
	fi, err := ql.log.Stat()
	if err == nil && fi.Size()+int64(len(p)) > qmpLogMaxSize {
		err = ql.rotate()
		if err != nil {
			return 0, err
		}
	}

	return ql.log.Write(p)
}

// rotate moves the log aside and starts a new one, the lock must be held.
func (ql *qmpLog) rotate() error {
	_ = ql.log.Close()
	ql.log = nil

	_ = os.Remove(ql.logFile + ".old")

	err := os.Rename(ql.logFile, ql.logFile+".old")
	if err != nil {
		return err
	}

	log, err := os.OpenFile(ql.logFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}

	ql.log = log

	return nil
}

// Close closes the log and wait the channel clean.
func (ql *qmpLog) Close() error {
	if ql == nil || ql.log == nil {
		return nil
	}

	ql.mu.Lock()
	defer ql.mu.Unlock()

	err := ql.log.Close()
	ql.log = nil
	return err
}
