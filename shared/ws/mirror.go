package ws

import (
	"io"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/shared/logger"
)

// Mirror takes a websocket and replicates all read/write to a ReadWriteCloser.
// Returns channels indicating when reads and writes are finished (respectively).
func Mirror(conn *websocket.Conn, rwc io.ReadWriteCloser) (chan struct{}, chan struct{}) {
	chRead := MirrorRead(conn, rwc)
	chWrite := MirrorWrite(conn, rwc)

	return chRead, chWrite
}

// MirrorRead is a uni-directional mirror which replicates an io.ReadCloser to a websocket.
func MirrorRead(conn *websocket.Conn, rc io.ReadCloser) chan struct{} {
	chDone := make(chan struct{}, 1)
	if rc == nil {
		close(chDone)
		return chDone
	}

	logger.Debug("Websocket: Started read mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

	connRWC := NewWrapper(conn)

	go func() {
		defer close(chDone)

		_, _ = io.Copy(connRWC, rc)

		logger.Debug("Websocket: Stopped read mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

		// Send write barrier.
		connRWC.Close()
	}()

	return chDone
}

// MirrorWrite is a uni-directional mirror which replicates a websocket to an io.WriteCloser.
func MirrorWrite(conn *websocket.Conn, wc io.WriteCloser) chan struct{} {
	chDone := make(chan struct{}, 1)
	if wc == nil {
		close(chDone)
		return chDone
	}

	logger.Debug("Websocket: Started write mirror", logger.Ctx{"address": conn.RemoteAddr().String()})

	connRWC := NewWrapper(conn)

	go func() {
		defer close(chDone)
		_, _ = io.Copy(wc, connRWC)

		logger.Debug("Websocket: Stopped write mirror", logger.Ctx{"address": conn.RemoteAddr().String()})
	}()

	return chDone
}
