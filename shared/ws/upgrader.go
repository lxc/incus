package ws

import (
	"time"

	"github.com/gorilla/websocket"
)

// Upgrader is a websocket upgrader which ignores the request Origin.
var Upgrader = websocket.Upgrader{
	HandshakeTimeout: time.Second * 5,
}
