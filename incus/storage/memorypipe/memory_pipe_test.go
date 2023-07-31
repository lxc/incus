package memorypipe

import (
	"bytes"
	"context"
	"testing"
)

// Test memorypipe.
func TestMemoryPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	aEnd, bEnd := NewPipePair(ctx)

	// Create four byte buffer and write it to pipe.
	sendMsg := []byte{1, 2, 3, 4}
	n, err := aEnd.Write(sendMsg)
	if err != nil {
		t.Errorf("Unexpected write error: %v", err)
	}

	if n != len(sendMsg) {
		t.Errorf("Unexpected write length: %d, expected: %d", n, len(sendMsg))
	}

	// Create two byte buffer and try to read from pipe. We should get half of the sent message.
	recvMsg := make([]byte, 2)
	n, err = bEnd.Read(recvMsg)
	if err != nil {
		t.Errorf("Unexpected read error: %v", err)
	}

	if n != len(recvMsg) {
		t.Errorf("Unexpected read length: %d, expected: %d", n, len(recvMsg))
	}

	if !bytes.Equal(recvMsg, sendMsg[:2]) {
		t.Errorf("Unexpected read contents: %v, expected: %v", recvMsg, sendMsg[:2])
	}

	// Now read again into the two byte buffer, we should get the remainder of the send message.
	n, err = bEnd.Read(recvMsg)
	if err != nil {
		t.Errorf("Unexpected read error: %v", err)
	}

	if n != len(recvMsg) {
		t.Errorf("Unexpected read length: %d, expected: %d", n, len(recvMsg))
	}

	if !bytes.Equal(recvMsg, sendMsg[2:]) {
		t.Errorf("Unexpected read contents: %v, expected: %v", recvMsg, sendMsg[2:])
	}

	// Send a new message.
	sendMsg = []byte{1, 2, 3, 4, 5}
	n, err = aEnd.Write(sendMsg)
	if err != nil {
		t.Errorf("Unexpected write error: %v", err)
	}

	if n != len(sendMsg) {
		t.Errorf("Unexpected write length: %d, expected: %d", n, len(sendMsg))
	}

	// Read entire message this time.
	recvMsg = make([]byte, len(sendMsg))
	n, err = bEnd.Read(recvMsg)
	if err != nil {
		t.Errorf("Unexpected read error: %v", err)
	}

	if n != len(recvMsg) {
		t.Errorf("Unexpected read length: %d, expected: %d", n, len(recvMsg))
	}

	if !bytes.Equal(recvMsg, sendMsg) {
		t.Errorf("Unexpected read contents: %v, expected: %v", recvMsg, sendMsg)
	}
}
