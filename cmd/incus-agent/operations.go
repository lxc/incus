package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/response"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/api"
)

var operationCmd = APIEndpoint{
	Path: "operations/{id}",

	Delete: APIEndpointAction{Handler: operationDelete},
	Get:    APIEndpointAction{Handler: operationGet},
}

var operationsCmd = APIEndpoint{
	Path: "operations",

	Get: APIEndpointAction{Handler: operationsGet},
}

var operationWebsocket = APIEndpoint{
	Path: "operations/{id}/websocket",

	Get: APIEndpointAction{Handler: operationWebsocketGet},
}

var operationWait = APIEndpoint{
	Path: "operations/{id}/wait",
	Get:  APIEndpointAction{Handler: operationWaitGet},
}

func operationDelete(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = op.Cancel()
	if err != nil {
		return response.BadRequest(err)
	}

	return response.EmptySyncResponse
}

func operationGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	_, body, err = op.Render()
	if err != nil {
		log.Println(fmt.Errorf("Failed to handle operations request: %w", err))
	}

	return response.SyncResponse(true, body)
}

func operationsGet(d *Daemon, r *http.Request) response.Response {
	recursion := localUtil.IsRecursionRequest(r)

	localOperationURLs := func() (jmap.Map, error) {
		// Get all the operations
		ops := operations.Clone()

		// Build a list of URLs
		body := jmap.Map{}

		for _, v := range ops {
			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]string, 0)
			}

			body[status] = append(body[status].([]string), v.URL())
		}

		return body, nil
	}

	localOperations := func() (jmap.Map, error) {
		// Get all the operations
		ops := operations.Clone()

		// Build a list of operations
		body := jmap.Map{}

		for _, v := range ops {
			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]*api.Operation, 0)
			}

			_, op, err := v.Render()
			if err != nil {
				return nil, err
			}

			body[status] = append(body[status].([]*api.Operation), op)
		}

		return body, nil
	}

	// Start with local operations
	var md jmap.Map
	var err error

	if recursion {
		md, err = localOperations()
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		md, err = localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}
	}

	return response.SyncResponse(true, md)
}

func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.OperationWebSocket(r, op)
}

func operationWaitGet(d *Daemon, r *http.Request) response.Response {
	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to extract operation ID from URL: %w", err))
	}

	timeoutSecs := -1
	if r.FormValue("timeout") != "" {
		timeoutSecs, err = strconv.Atoi(r.FormValue("timeout"))
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to extract operation wait timeout from URL: %w", err))
		}
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if timeoutSecs > -1 {
		ctx, cancel = context.WithDeadline(r.Context(), time.Now().Add(time.Second*time.Duration(timeoutSecs)))
	} else {
		ctx, cancel = context.WithCancel(r.Context())
	}

	defer cancel()

	op, err := operations.OperationGetInternal(id)
	if err != nil {
		return response.NotFound(err)
	}

	err = op.Wait(ctx)
	if err != nil {
		return response.SmartError(err)
	}

	_, opAPI, err := op.Render()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, opAPI)
}
