package migration

import (
	"errors"
	"io"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	internalIO "github.com/lxc/incus/v7/internal/io"
	"github.com/lxc/incus/v7/shared/logger"
)

// ProtoRecv gets a protobuf message from a websocket.
func ProtoRecv(ws *websocket.Conn, msg proto.Message) error {
	buf, err := protoRecvRaw(ws)
	if err != nil {
		return err
	}

	return proto.Unmarshal(buf, msg)
}

// protoRecvRaw reads the raw bytes of a single binary message from a websocket.
func protoRecvRaw(ws *websocket.Conn) ([]byte, error) {
	if ws == nil {
		return nil, errors.New("Empty websocket connection")
	}

	mt, r, err := ws.NextReader()
	if err != nil {
		return nil, err
	}

	if mt != websocket.BinaryMessage {
		return nil, errors.New("Only binary messages allowed")
	}

	return io.ReadAll(r)
}

// ProtoRecvHeader acts like ProtoRecv but knows to expect a header and validates it for errors.
func ProtoRecvHeader(ws *websocket.Conn, msg proto.Message) error {
	buf, err := protoRecvRaw(ws)
	if err != nil {
		return err
	}

	// Check whether the peer actually sent a control failure message. A genuine
	// header never carries field 2 as a length-delimited value, so a non-empty
	// message field unambiguously identifies a MigrationControl failure.
	control := MigrationControl{}
	err = proto.Unmarshal(buf, &control)
	if err == nil && !control.GetSuccess() && control.GetMessage() != "" {
		return errors.New(control.GetMessage())
	}

	return proto.Unmarshal(buf, msg)
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

	defer logger.WarnOnError(w.Close, "Failed to close websocket writer")

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
