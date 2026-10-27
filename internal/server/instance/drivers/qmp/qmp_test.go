package qmp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	m := &qemuMachineProtocol{}
	mockMonitorServer(t, eg, m)

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

	m := &qemuMachineProtocol{}
	mockMonitorServer(t, eg, m, func(nc net.Conn) error {
		enc := json.NewEncoder(nc)
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
	mon := &qemuMachineProtocol{}

	r := strings.NewReader("")

	events := make(chan qmpEvent)
	replies := &mon.replies

	mon.listen(r, events, replies)

	_, ok := <-events
	if ok {
		t.Fatal("events channel should be closed")
	}

	replies.Range(func(key, value any) bool {
		t.Fatal("replies should be empty")
		return false
	})
}

func TestListenScannerErr(t *testing.T) {
	mon := &qemuMachineProtocol{}

	errFoo := errors.New("foo")
	r := &testingErrReader{err: errFoo}

	events := make(chan qmpEvent)
	replies := &mon.replies

	repCh := make(chan rawResponse, 1)
	replies.Store(0, repCh)

	mon.listen(r, events, replies)

	res := <-repCh
	if errFoo != res.err {
		t.Fatalf("unexpected error:\n- want: %v\n-  got: %v", errFoo, res.err)
	}
}

func TestListenInvalidJson(t *testing.T) {
	mon := &qemuMachineProtocol{}

	r := strings.NewReader("<html>")

	events := make(chan qmpEvent)
	replies := &mon.replies

	mon.listen(r, events, replies)

	replies.Range(func(key, value any) bool {
		t.Fatal("replies should be empty")
		return false
	})
}

func TestListenStreamResponse(t *testing.T) {
	mon := &qemuMachineProtocol{}
	id := uint32(1)
	want := `{"foo": "bar", "id": 1}`
	r := strings.NewReader(want)

	events := make(chan qmpEvent)
	replies := &mon.replies
	repCh := make(chan rawResponse, 1)
	replies.Store(id, repCh)
	go mon.listen(r, events, replies)
	res := <-repCh
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}

	got := string(res.raw)
	if want != got {
		t.Fatalf("unexpected response:\n- want: %q\n-  got: %q", want, got)
	}
}

func TestListenEventNoListeners(t *testing.T) {
	mon := &qemuMachineProtocol{}

	r := strings.NewReader(`{"event":"STOP"}`)

	events := make(chan qmpEvent)
	replies := &mon.replies

	go mon.listen(r, events, replies)

	_, ok := <-events
	if ok {
		t.Fatal("events channel should be closed")
	}
}

func TestListenEventOneListener(t *testing.T) {
	mon := &qemuMachineProtocol{}
	mon.listeners.Store(1)

	eventStop := "STOP"
	r := strings.NewReader(fmt.Sprintf(`{"event":%q}`, eventStop))

	events := make(chan qmpEvent)
	replies := &mon.replies

	go mon.listen(r, events, replies)

	e := <-events
	want, got := eventStop, e.Event
	if want != got {
		t.Fatalf("unexpected event:\n- want: %q\n-  got: %q", want, got)
	}
}

func mockMonitorServer(t *testing.T, eg *errgroup.Group, qmp *qemuMachineProtocol, hands ...func(net.Conn) error) {
	t.Helper()
	unixsock := filepath.Join(t.TempDir(), "mockmonitor.sock")
	unixaddr, err := net.ResolveUnixAddr("unix", unixsock)
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.ListenUnix("unix", unixaddr)
	if err != nil {
		t.Fatal(err)
	}

	eg.Go(func() error {
		tc, err := l.Accept()
		if err != nil {
			t.Log(err)
			return err
		}

		enc := json.NewEncoder(tc)
		dec := json.NewDecoder(tc)
		err = enc.Encode(testingGreeting)
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

		// wait client listen ready
		for qmp.events == nil {
			time.Sleep(time.Millisecond * 10)
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

	for qmp.uc == nil {
		uc, err := net.DialUnix("unix", nil, unixaddr)
		if err != nil {
			// Wait unix socket being created
			t.Log(err)
			time.Sleep(time.Millisecond * 10)
			continue
		}

		qmp.uc = uc
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = l.Close()
	})
}
