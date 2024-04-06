package main

import (
	"net/http"

	"github.com/lxc/incus/v6/shared/api"
)

type devIncusResponse struct {
	content any
	code    int
	ctype   string
}

func errorResponse(code int, msg string) *devIncusResponse {
	return &devIncusResponse{msg, code, "raw"}
}

func okResponse(ct any, ctype string) *devIncusResponse {
	return &devIncusResponse{ct, http.StatusOK, ctype}
}

func smartResponse(err error) *devIncusResponse {
	if err == nil {
		return okResponse(nil, "")
	}

	statusCode, found := api.StatusErrorMatch(err)
	if found {
		return errorResponse(statusCode, err.Error())
	}

	return errorResponse(http.StatusInternalServerError, err.Error())
}
