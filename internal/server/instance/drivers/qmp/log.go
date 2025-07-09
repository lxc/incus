package qmp

import (
	"fmt"
	"os"
	"sync"
)

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
	ql.mu.Lock()
	defer ql.mu.Unlock()

	// Ignore writes after close.
	if ql.log == nil {
		return 0, nil
	}

	return ql.log.Write(p)
}

// Close closes the log and wait the channel clean.
func (ql *qmpLog) Close() error {
	if ql.log != nil {
		ql.mu.Lock()
		defer ql.mu.Unlock()

		err := ql.log.Close()
		ql.log = nil
		return err
	}

	return nil
}
