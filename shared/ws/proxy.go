package ws

import (
	"io"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/logger"
)

// Proxy mirrors the traffic between two websockets.
func Proxy(source *websocket.Conn, target *websocket.Conn) chan struct{} {
	logger.Debug("Websocket: Started proxy", logger.Ctx{"source": source.RemoteAddr().String(), "target": target.RemoteAddr().String()})

	// Forwarder between two websockets, closes channel upon disconnection.
	forward := func(in *websocket.Conn, out *websocket.Conn, ch chan struct{}) {
		for {
			mt, r, err := in.NextReader()
			if err != nil {
				break
			}

			w, err := out.NextWriter(mt)
			if err != nil {
				break
			}

			_, err = io.Copy(w, r)
			w.Close()
			if err != nil {
				break
			}
		}

		close(ch)
	}

	// Spawn forwarders in both directions.
	chSend := make(chan struct{})
	go forward(source, target, chSend)

	chRecv := make(chan struct{})
	go forward(target, source, chRecv)

	// Close main channel and disconnect upon completion of either forwarder.
	ch := make(chan struct{})
	go func() {
		select {
		case <-chSend:
		case <-chRecv:
		}

		source.Close()
		target.Close()

		logger.Debug("Websocket: Stopped proxy", logger.Ctx{"source": source.RemoteAddr().String(), "target": target.RemoteAddr().String()})
		close(ch)
	}()

	return ch
}
