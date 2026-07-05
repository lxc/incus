package ws

import (
	"time"

	"github.com/gorilla/websocket"
)

// ControlMessageMaxSize is the maximum size of a websocket control message.
const ControlMessageMaxSize = 1024 * 32

// Upgrader is a websocket upgrader which ignores the request Origin.
var Upgrader = websocket.Upgrader{
	HandshakeTimeout: time.Second * 5,
}
