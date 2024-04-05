package operations

import (
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// Operation response.
type operationResponse struct {
	op *Operation
}

// OperationResponse returns an operation response.
func OperationResponse(op *Operation) response.Response {
	return &operationResponse{op}
}

func (r *operationResponse) Render(w http.ResponseWriter) error {
	err := r.op.Start()
	if err != nil {
		return err
	}

	url, md, err := r.op.Render()
	if err != nil {
		return err
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   md,
	}

	w.Header().Set("Location", url)

	code := 202
	w.WriteHeader(code)

	var debugLogger logger.Logger
	if debug {
		debugLogger = logger.AddContext(logger.Ctx{"http_code": code})
	}

	return localUtil.WriteJSON(w, body, debugLogger)
}

func (r *operationResponse) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

// Forwarded operation response.
//
// Returned when the operation has been created on another node.
type forwardedOperationResponse struct {
	op      *api.Operation
	project string
}

// ForwardedOperationResponse creates a response that forwards the metadata of
// an operation created on another node.
func ForwardedOperationResponse(project string, op *api.Operation) response.Response {
	return &forwardedOperationResponse{
		op:      op,
		project: project,
	}
}

func (r *forwardedOperationResponse) Render(w http.ResponseWriter) error {
	url := fmt.Sprintf("/%s/operations/%s", version.APIVersion, r.op.ID)
	if r.project != "" {
		url += fmt.Sprintf("?project=%s", r.project)
	}

	body := api.ResponseRaw{
		Type:       api.AsyncResponse,
		Status:     api.OperationCreated.String(),
		StatusCode: int(api.OperationCreated),
		Operation:  url,
		Metadata:   r.op,
	}

	w.Header().Set("Location", url)

	code := 202
	w.WriteHeader(code)

	var debugLogger logger.Logger
	if debug {
		debugLogger = logger.AddContext(logger.Ctx{"http_code": code})
	}

	return localUtil.WriteJSON(w, body, debugLogger)
}

func (r *forwardedOperationResponse) String() string {
	return r.op.ID
}
