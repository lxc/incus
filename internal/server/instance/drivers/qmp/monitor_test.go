package qmp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

var greeting = map[string]any{
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

type errReader struct {
	err error
}

func (r *errReader) Read(b []byte) (int, error) {
	return 0, r.err
}

func mockMonitorServer(t *testing.T, eg *errgroup.Group, hands ...func(net.Conn) error) *Monitor {
	t.Helper()
	sc, tc := net.Pipe()

	m := &Monitor{
		c: sc,
	}

	eg.Go(func() error {
		enc := json.NewEncoder(tc)
		dec := json.NewDecoder(tc)
		err := enc.Encode(greeting)
		if err != nil {
			t.Logf("unexpected error: %v", err)
			return err
		}

		var cmd Command
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

		err = enc.Encode(Response{ID: cmd.ID})
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
	es := []Event{
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

	events, err := m.Events(context.Background())
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
	m := &Monitor{}

	r := strings.NewReader("")

	events := make(chan Event)
	replies := &m.replies
	m.listen(r, events, replies)

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
	m := &Monitor{}

	want := errors.New("foo")
	r := &errReader{err: want}

	events := make(chan Event)
	replies := &m.replies

	m.listen(r, events, replies)
	val, ok := replies.LoadAndDelete(ZeroKey)
	if !ok {
		t.Fatal("No error found")
	}

	errCh, ok := val.(chan rawResponse)
	if !ok {
		t.Fatal("No error found")
	}

	res := <-errCh
	got := res.err

	if want != got {
		t.Fatalf("unexpected error:\n- want: %v\n-  got: %v", want, got)
	}
}

func TestListenInvalidJSON(t *testing.T) {
	m := &Monitor{}

	r := strings.NewReader("<html>")

	events := make(chan Event)
	replies := &m.replies

	m.listen(r, events, replies)

	replies.Range(func(key, value any) bool {
		t.Fatal("replies should be empty")
		return false
	})
}

func TestListenResponse(t *testing.T) {
	m := &Monitor{}

	id := uint32(1)
	want := `{"foo": "bar", "id": 1}`
	r := strings.NewReader(want)

	events := make(chan Event)
	replies := &m.replies
	repCh := make(chan rawResponse, 1)
	replies.Store(id, repCh)

	go m.listen(r, events, replies)

	res := <-repCh
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}

	got := string(res.b)
	if want != got {
		t.Fatalf("unexpected response:\n- want: %q\n-  got: %q", want, got)
	}
}

func TestListenEventNoListeners(t *testing.T) {
	m := &Monitor{}

	r := strings.NewReader(`{"event":"STOP"}`)

	events := make(chan Event)
	replies := &m.replies

	// listen is not blocking even if no listener
	m.listen(r, events, replies)

	_, ok := <-events
	if ok {
		t.Fatal("events channel should be closed")
	}
}

func TestListenEventOneListener(t *testing.T) {
	m := &Monitor{}

	eventStop := "STOP"
	r := strings.NewReader(fmt.Sprintf(`{"event":%q}`, eventStop))

	events := make(chan Event, 1)
	replies := &m.replies

	m.listen(r, events, replies)
	m.events = events
	e := <-events

	if eventStop != e.Event {
		t.Fatalf("unexpected event:\n- want: %q\n-  got: %q", eventStop, e.Event)
	}
}

func TestIncreaseID(t *testing.T) {
	m := &Monitor{}
	id := m.increaseID()
	if id != 1 {
		t.Fatal(id)
	}

	id = m.increaseID()
	if id != 2 {
		t.Fatal(id)
	}

	m.id.Store(uint32(0xffffffff))
	id = m.increaseID()
	if id != 1 { // skip 0
		t.Fatal(id, uint32(0xffffffff))
	}
}

func TestReqLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "qemu.qmp.log")
	m := &Monitor{
		logPath: logPath,
	}

	err := m.initQMPLog()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("canLog", func(t *testing.T) {
		req := &Command{Execute: "query-version"}
		logf := m.queryLog(req)

		if logf == nil {
			t.Fatal()
		}

		_, err := logf("QUERY: %s %v", req.Execute, req.Arguments)
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%s", b)
	})

	t.Run("noLog", func(t *testing.T) {
		req := &Command{Execute: "ringbuf-read"}
		logf := m.queryLog(req)

		if logf != nil {
			t.Fatal()
		}
	})

	t.Run("logok", func(t *testing.T) {
		req := &Command{Execute: "ringbuf-read", logok: true}
		logf := m.queryLog(req)
		if logf == nil {
			t.Fatal()
		}

		_, err := logf("QUERY: %s %v", req.Execute, req.Arguments)
		if err != nil {
			t.Fatal(err)
		}

		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}

		t.Logf("%s", b)
	})
}

func TestEventLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "qemu.qmp.log")
	m := &Monitor{
		logPath: logPath,
	}

	err := m.initQMPLog()
	if err != nil {
		t.Fatal(err)
	}

	tcs := []struct {
		name    string
		event   string
		debug   string
		wantNil bool
	}{
		{"Empty event", "", "1", true},
		{"No debug", "RTC_CHANGE", "0", true},
		{"Normal", "RTC_CHANGE", "1", false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			e := &Event{Event: tc.event}
			err = os.Setenv("INCUS_QMP_EVENTS_DEBUG", tc.debug)
			if err != nil {
				t.Fatal(err)
			}

			logf := m.eventLog(e)
			if (logf == nil) != tc.wantNil {
				t.Fatal(logf == nil, tc.wantNil)
			}
		})
	}
}

func TestRunJSON(t *testing.T) {
	eg := &errgroup.Group{}
	m := mockMonitorServer(t, eg, func(tc net.Conn) error {
		dec := json.NewDecoder(tc)
		req := &Command{}
		err := dec.Decode(req)
		if err != nil {
			t.Log(err)
			return err
		}

		id := req.ID
		if id == 0 {
			return fmt.Errorf("zero id found")
		}

		rep := Response{
			ID: id,
			Return: map[string]any{
				"status":  "running",
				"running": true,
			},
		}

		enc := json.NewEncoder(tc)
		err = enc.Encode(rep)
		if err != nil {
			return err
		}

		return nil
	})
	err := m.connect()
	if err != nil {
		t.Fatal(err)
	}

	queryStatus := `{"execute":"query-status"}`
	status := &struct {
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}{}

	rep := &Response{
		Return: status,
	}

	err = m.RunJSON([]byte(queryStatus), rep, false)
	if err != nil {
		t.Fatal(err)
	}

	if rep.ID == 0 {
		t.Fatal()
	}

	if !status.Running || status.Status != "running" {
		t.Fatal(status)
	}
}
