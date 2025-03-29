package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Upgrader is a websocket upgrader which ignores the request Origin.
var Upgrader = websocket.Upgrader{
	CheckOrigin:      func(_ *http.Request) bool { return true },
	HandshakeTimeout: time.Second * 5,
}
