package main

import (
	"net/http"

	"github.com/lxc/incus/shared/api"
)

type devLxdResponse struct {
	content any
	code    int
	ctype   string
}

func errorResponse(code int, msg string) *devLxdResponse {
	return &devLxdResponse{msg, code, "raw"}
}

func okResponse(ct any, ctype string) *devLxdResponse {
	return &devLxdResponse{ct, http.StatusOK, ctype}
}

func smartResponse(err error) *devLxdResponse {
	if err == nil {
		return okResponse(nil, "")
	}

	statusCode, found := api.StatusErrorMatch(err)
	if found {
		return errorResponse(statusCode, err.Error())
	}

	return errorResponse(http.StatusInternalServerError, err.Error())
}
