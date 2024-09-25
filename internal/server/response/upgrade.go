package response

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Upgrade takes a hijacked HTTP connection and sends the HTTP 101 Switching Protocols headers for protocolName.
func Upgrade(hijackedConn net.Conn, protocolName string) error {
	// Write the status line and upgrade header by hand since w.WriteHeader() would fail after Hijack().
	sb := strings.Builder{}
	sb.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	sb.WriteString(fmt.Sprintf("Upgrade: %s\r\n", protocolName))
	sb.WriteString("Connection: Upgrade\r\n\r\n")

	_ = hijackedConn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	n, err := hijackedConn.Write([]byte(sb.String()))
	_ = hijackedConn.SetWriteDeadline(time.Time{}) // Cancel deadline.

	if err != nil {
		return fmt.Errorf("Failed writing upgrade headers: %w", err)
	}

	if n != sb.Len() {
		return fmt.Errorf("Failed writing upgrade headers")
	}

	return nil
}
