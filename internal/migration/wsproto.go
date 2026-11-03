package migration

import (
	"errors"
	"fmt"
	"io"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	internalIO "github.com/lxc/incus/v6/internal/io"
)

// MessageMaxSize is the maximum size of a single migration protocol message.
const MessageMaxSize = 32 * 1024 * 1024

// ProtoRecv gets a protobuf message from a websocket.
func ProtoRecv(ws *websocket.Conn, msg proto.Message) error {
	if ws == nil {
		return errors.New("Empty websocket connection")
	}

	mt, r, err := ws.NextReader()
	if err != nil {
		return err
	}

	if mt != websocket.BinaryMessage {
		return errors.New("Only binary messages allowed")
	}

	// Bound the message size, the peer isn't trusted.
	buf, err := io.ReadAll(io.LimitReader(r, MessageMaxSize+1))
	if err != nil {
		return err
	}

	if len(buf) > MessageMaxSize {
		return fmt.Errorf("Migration message exceeds the maximum size of %d bytes", MessageMaxSize)
	}

	err = proto.Unmarshal(buf, msg)
	if err != nil {
		return err
	}

	return nil
}

// ProtoSend sends a protobuf message over a websocket.
func ProtoSend(ws *websocket.Conn, msg proto.Message) error {
	if ws == nil {
		return errors.New("Empty websocket connection")
	}

	w, err := ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}

	defer func() { _ = w.Close() }()

	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	err = internalIO.WriteAll(w, data)
	if err != nil {
		return err
	}

	return w.Close()
}

// ProtoSendControl sends a migration control message over a websocket.
func ProtoSendControl(ws *websocket.Conn, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}

	msg := MigrationControl{
		Success: proto.Bool(err == nil),
		Message: proto.String(message),
	}

	_ = ProtoSend(ws, &msg)
}
