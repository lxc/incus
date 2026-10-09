//go:build linux && cgo && !agent

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

type networkOVNReleaseTestLogger struct {
	logger.Logger
	messages []string
	contexts []logger.Ctx
}

func (l *networkOVNReleaseTestLogger) Error(message string, contexts ...logger.Ctx) {
	l.messages = append(l.messages, message)
	l.contexts = append(l.contexts, contexts...)
}

func TestNetworkOVNReleaseResponse(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response func() response.Response
	}{
		{"timeout", func() response.Response {
			return response.ErrorResponse(http.StatusGatewayTimeout, context.DeadlineExceeded.Error())
		}},
		{"canceled", func() response.Response { return response.SmartError(context.Canceled) }},
		{"unavailable", func() response.Response {
			return response.Unavailable(errors.New("OVN backend unavailable"))
		}},
		{"unsupported", func() response.Response {
			return response.NotImplemented(errors.New("Operation is unsupported"))
		}},
		{"forbidden", func() response.Response { return response.Forbidden(errors.New("Access denied")) }},
		{"raw error", func() response.Response {
			return response.DevIncusErrorResponse(api.StatusErrorf(http.StatusConflict, "Original conflict"), false)
		}},
		{"success", func() response.Response { return response.SyncResponse(true, "original success") }},
		{"nil", func() response.Response { return nil }},
	} {
		for _, releaseFails := range []bool{false, true} {
			releaseName := "release success"
			if releaseFails {
				releaseName = "release failure"
			}

			t.Run(tt.name+"/"+releaseName, func(t *testing.T) {
				log := &networkOVNReleaseTestLogger{}
				originalLogger := logger.Log
				logger.Log = log
				t.Cleanup(func() { logger.Log = originalLogger })

				original := tt.response()
				var before *httptest.ResponseRecorder
				if original != nil {
					before = httptest.NewRecorder()
					require.NoError(t, original.Render(before))
				}

				releaseErr := errors.New("Receipt release failed")
				calls := 0
				result := original
				networkOVNReleaseResponse(func() error {
					calls++
					if releaseFails {
						return releaseErr
					}

					return nil
				}, &result)
				require.Equal(t, 1, calls)

				preserveFailure := before != nil && before.Code >= http.StatusBadRequest
				if !releaseFails || preserveFailure {
					if original == nil {
						require.Nil(t, result)
					} else {
						require.Same(t, original, result)
					}

					if before != nil {
						after := httptest.NewRecorder()
						require.NoError(t, result.Render(after))
						require.Equal(t, before.Code, after.Code)
						require.Equal(t, before.Header(), after.Header())
						require.Equal(t, before.Body.String(), after.Body.String())
					}
				} else {
					after := httptest.NewRecorder()
					require.NoError(t, result.Render(after))
					require.Equal(t, http.StatusInternalServerError, after.Code)
					require.Contains(t, after.Body.String(), "Failed releasing OVN network operation: Receipt release failed")
				}

				if releaseFails && preserveFailure {
					require.Equal(t, []string{"Failed releasing OVN network operation"}, log.messages)
					require.Equal(t, []logger.Ctx{{"err": releaseErr}}, log.contexts)
				} else {
					require.Empty(t, log.messages)
				}
			})
		}
	}
}
