package ws

import (
	"io"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/logger"
)

// NewWrapper returns a new ReadWriteCloser wrapper for a websocket connection.
func NewWrapper(conn *websocket.Conn) io.ReadWriteCloser {
	return &wrapper{conn: conn}
}

// wrapper implements ReadWriteCloser on top of a websocket connection.
type wrapper struct {
	conn   *websocket.Conn
	reader io.Reader
	mur    sync.Mutex
	muw    sync.Mutex
}

func (w *wrapper) Read(p []byte) (n int, err error) {
	w.mur.Lock()
	defer w.mur.Unlock()

	// Get new message if no active one.
	if w.reader == nil {
		var mt int

		mt, w.reader, err = w.conn.NextReader()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				logger.Debug("Websocket: Got normal closure", logger.Ctx{"address": w.conn.RemoteAddr().String()})
				return 0, io.EOF
			}

			return 0, err
		}

		if mt == websocket.CloseMessage || mt == websocket.TextMessage {
			switch mt {
			case websocket.CloseMessage:
				logger.Debug("Websocket: Got close message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
			case websocket.TextMessage:
				logger.Debug("Websocket: Got barrier message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
			}

			w.reader = nil // At the end of the message, reset reader.

			return 0, io.EOF
		}
	}

	// Perform the read itself.
	n, err = w.reader.Read(p)
	if err != nil {
		w.reader = nil // At the end of the message, reset reader.

		if err == io.EOF {
			return n, nil // Don't return EOF error at end of message.
		}

		return n, err
	}

	return n, nil
}

func (w *wrapper) Write(p []byte) (int, error) {
	w.muw.Lock()
	defer w.muw.Unlock()

	// Send the data as a binary message.
	err := w.conn.WriteMessage(websocket.BinaryMessage, p)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

// Close sends a control message indicating the stream is finished, but it does not actually close the socket.
func (w *wrapper) Close() error {
	w.muw.Lock()
	defer w.muw.Unlock()

	// Send the barrier message (don't actually close the socket so we can use it for another stream).
	logger.Debug("Websocket: Sending barrier message", logger.Ctx{"address": w.conn.RemoteAddr().String()})
	return w.conn.WriteMessage(websocket.TextMessage, []byte{})
}
