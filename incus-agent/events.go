package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/response"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/ws"
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get:  APIEndpointAction{Handler: eventsGet},
	Post: APIEndpointAction{Handler: eventsPost},
}

type eventsServe struct {
	req *http.Request
	d   *Daemon
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.d, r.req, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

func eventsSocket(d *Daemon, r *http.Request, w http.ResponseWriter) error {
	typeStr := r.FormValue("type")
	if typeStr == "" {
		// We add 'config' here to allow listeners on /dev/lxd/sock to receive config changes.
		typeStr = "logging,operation,lifecycle,config"
	}

	var listenerConnection events.EventListenerConnection

	// If the client has not requested a websocket connection then fallback to long polling event stream mode.
	if r.Header.Get("Upgrade") == "websocket" {
		// Upgrade the connection to websocket
		conn, err := ws.Upgrader.Upgrade(w, r, nil)
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection = events.NewWebsocketListenerConnection(conn)
	} else {
		h, ok := w.(http.Hijacker)
		if !ok {
			return fmt.Errorf("Missing implemented http.Hijacker interface")
		}

		conn, _, err := h.Hijack()
		if err != nil {
			return err
		}

		defer func() { _ = conn.Close() }() // Ensure listener below ends when this function ends.

		listenerConnection, err = events.NewStreamListenerConnection(conn)
		if err != nil {
			return err
		}
	}

	// As we don't know which project we are in, subscribe to events from all projects.
	listener, err := d.events.AddListener("", true, listenerConnection, strings.Split(typeStr, ","), nil, nil, nil)
	if err != nil {
		return err
	}

	listener.Wait(r.Context())

	return nil
}

func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, d: d}
}

func eventsPost(d *Daemon, r *http.Request) response.Response {
	var event api.Event

	err := json.NewDecoder(r.Body).Decode(&event)
	if err != nil {
		return response.InternalError(err)
	}

	err = d.events.Send("", event.Type, event.Metadata)
	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, nil)
}
