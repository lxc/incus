package memorypipe

import (
	"context"
	"io"
)

const bufferSize = 10

// msg represents an internal structure sent between the pipes.
type msg struct {
	data []byte
	err  error
}

// pipe provides a bidirectional pipe compatible with io.ReadWriteCloser interface.
// Note, however, that it does not behave exactly how one would expect an io.ReadWriteCloser to
// behave. Specifically the Close() function does not close the pipe, but instead delivers an io.EOF
// error to the next reader. After which it can be read again to receive new data. This means the
// pipe can be closed multiple times. Each time it indicates that one particular session has ended.
// The reason for this is to emulate the WebsocketIO's behaviour by allowing a single persistent
// connection to be used for multiple sessions.
type pipe struct {
	ch           chan msg
	ctx          context.Context
	otherEnd     *pipe
	msgRemainder []byte
}

// Read reads from the pipe into p. Returns number of bytes read and any errors.
func (p *pipe) Read(b []byte) (int, error) {
	if p.msgRemainder != nil {
		n := copy(b, p.msgRemainder)
		if len(p.msgRemainder) > n {
			tmpBuf := make([]byte, len(p.msgRemainder)-n)
			copy(tmpBuf, p.msgRemainder[n:])
			p.msgRemainder = tmpBuf
		} else {
			p.msgRemainder = nil
		}

		return n, nil
	}

	select {
	case msg := <-p.ch:
		if msg.err == io.EOF {
			return 0, msg.err
		}

		n := copy(b, msg.data)

		// Store the remainder of the message for next Read.
		if len(msg.data) > n {
			p.msgRemainder = make([]byte, len(msg.data)-n)
			copy(p.msgRemainder, msg.data[n:])
		} else {
			p.msgRemainder = nil
		}

		return n, msg.err
	case <-p.ctx.Done():
		return 0, p.ctx.Err()
	}
}

// Write writes to the pipe from p. Returns number of bytes written and any errors.
func (p *pipe) Write(b []byte) (int, error) {
	msg := msg{
		data: make([]byte, len(b)),
		err:  nil,
	}

	// Create copy of b in case it is modified externally.
	copy(msg.data, b)

	select {
	case p.otherEnd.ch <- msg: // Sent msg to the other side's Read function.
		return len(msg.data), msg.err
	case <-p.ctx.Done():
		return 0, p.ctx.Err()
	}
}

// Close is unusual in that it doesn't actually close the pipe. Instead it sends an io.EOF error
// to the other side's Read function. This is so the other side can detect that a session has ended.
// Each call to Close will indicate to the other side that a session has ended, whilst allowing the
// reuse of a single persistent pipe for multiple sessions.
func (p *pipe) Close() error {
	msg := msg{
		data: nil,
		err:  io.EOF, // Indicates to the other side's Read function that session has ended.
	}

	select {
	case p.otherEnd.ch <- msg: // Sent msg to the other side's Read function.
		return nil
	case <-p.ctx.Done():
		return p.ctx.Err()
	}
}

// NewPipePair returns a pair of io.ReadWriterCloser pipes that are connected together such that
// writes to one will appear as reads on the other and vice versa. Calling Close() on one end will
// indicate to the other end that the session has ended.
func NewPipePair(ctx context.Context) (io.ReadWriteCloser, io.ReadWriteCloser) {
	aEnd := &pipe{
		ch:  make(chan msg, bufferSize),
		ctx: ctx,
	}

	bEnd := &pipe{
		ch:  make(chan msg, bufferSize),
		ctx: ctx,
	}

	aEnd.otherEnd = bEnd
	bEnd.otherEnd = aEnd
	return aEnd, bEnd
}
