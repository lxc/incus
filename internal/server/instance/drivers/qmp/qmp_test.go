package qmp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

var testingGreeting = map[string]any{
	"QMP": map[string]any{
		"version": map[string]any{
			"qemu": map[string]any{
				"micro": 2,
				"minor": 2,
				"major": 9,
			},
			"package": "v9.2.2",
		},
		"capabilities": []string{"oob"},
	},
}

type testingErrReader struct {
	err error
}

func (r *testingErrReader) Read(b []byte) (int, error) {
	return 0, r.err
}

func TestConnectDisconnect(t *testing.T) {
	eg := &errgroup.Group{}
	m := mockMonitorServer(t, eg)

	err := m.connect()
	if err != nil {
		t.Fatal(err)
	}

	err = m.disconnect()
	if err != nil {
		t.Fatal(err)
	}

	err = eg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEvents(t *testing.T) {
	eg := &errgroup.Group{}
	es := []qmpEvent{
		{Event: "STOP"},
		{Event: "SHUTDOWN"},
		{Event: "RESET"},
	}

	m := mockMonitorServer(t, eg, func(tc net.Conn) error {
		enc := json.NewEncoder(tc)
		for i, e := range es {
			err := enc.Encode(e)
			if err != nil {
				t.Log(i, e, err)
				return err
			}
		}

		return nil
	})

	err := m.connect()
	if err != nil {
		t.Fatal(err)
	}

	events, err := m.getEvents(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i, want := range es {
		got := <-events
		if !reflect.DeepEqual(want, got) {
			t.Fatal(i, want, got)
		}
	}

	err = eg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

func TestListenEmptyStream(t *testing.T) {
	mon := &qemuMachineProtocal{}

	r := strings.NewReader("")

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	mon.listen(r, events, stream)

	_, ok := <-events
	if ok {
		t.Fatal("events channel should be closed")
	}

	_, ok = <-stream
	if ok {
		t.Fatal("stream channel should be closed")
	}
}

func TestListenScannerErr(t *testing.T) {
	mon := &qemuMachineProtocal{}

	errFoo := errors.New("foo")
	r := &testingErrReader{err: errFoo}

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	go mon.listen(r, events, stream)
	res := <-stream

	if errFoo != res.err {
		t.Fatalf("unexpected error:\n- want: %v\n-  got: %v", errFoo, res.err)
	}
}

func TestListenInvalidJson(t *testing.T) {
	mon := &qemuMachineProtocal{}

	r := strings.NewReader("<html>")

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	mon.listen(r, events, stream)

	_, ok := <-stream
	if ok {
		t.Fatal("stream channel should be closed")
	}
}

func TestListenStreamResponse(t *testing.T) {
	mon := &qemuMachineProtocal{}

	want := `{"foo": "bar"}`
	r := strings.NewReader(want)

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	go mon.listen(r, events, stream)

	res := <-stream
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}

	got := string(res.raw)
	if want != got {
		t.Fatalf("unexpected response:\n- want: %q\n-  got: %q", want, got)
	}
}

func TestListenEventNoListeners(t *testing.T) {
	mon := &qemuMachineProtocal{}

	r := strings.NewReader(`{"event":"STOP"}`)

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	go mon.listen(r, events, stream)

	_, ok := <-events
	if ok {
		t.Fatal("events channel should be closed")
	}
}

func TestListenEventOneListener(t *testing.T) {
	mon := &qemuMachineProtocal{}
	mon.listeners.Store(1)

	eventStop := "STOP"
	r := strings.NewReader(fmt.Sprintf(`{"event":%q}`, eventStop))

	events := make(chan qmpEvent)
	stream := make(chan rawResponse)

	go mon.listen(r, events, stream)

	e := <-events
	want, got := eventStop, e.Event
	if want != got {
		t.Fatalf("unexpected event:\n- want: %q\n-  got: %q", want, got)
	}
}

func mockMonitorServer(t *testing.T, eg *errgroup.Group, hands ...func(net.Conn) error) *qemuMachineProtocal {
	t.Helper()
	sc, tc := net.Pipe()

	m := &qemuMachineProtocal{
		c: sc,
	}

	eg.Go(func() error {
		enc := json.NewEncoder(tc)
		dec := json.NewDecoder(tc)
		err := enc.Encode(testingGreeting)
		if err != nil {
			t.Logf("unexpected error: %v", err)
			return err
		}

		var cmd qmpCommand
		err = dec.Decode(&cmd)
		if err != nil {
			err = fmt.Errorf("unexpected error: %w", err)
			t.Log(err)
			return err
		}

		if cmd.Execute != "qmp_capabilities" {
			err = fmt.Errorf("unexpected capabilities handshake:\n- want: %q\n-  got: %q",
				"qmp_capabilities", cmd.Execute)
			t.Log(err)
			return err
		}

		err = enc.Encode(qmpResponse{ID: cmd.ID})
		if err != nil {
			err = fmt.Errorf("unexpected error: %w", err)
			t.Log(err)
			return err
		}

		for i, hand := range hands {
			err = hand(tc)
			if err != nil {
				t.Log(i, err)
				return err
			}
		}

		return err
	})

	return m
}
