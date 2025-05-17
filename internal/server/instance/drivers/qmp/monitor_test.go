package qmp

import (
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"golang.org/x/sync/errgroup"
)

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
	err := m.qmpConnect()
	if err != nil {
		t.Fatal(err)
	}

	queryStatus := map[string]any{"execute": "query-status"}
	status := &struct {
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}{}

	rep := &Response{
		Return: status,
	}

	err = m.RunJSON(queryStatus, rep)
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
