//go:build linux && cgo && !agent

package response

import (
	"net/http"

	"github.com/cowsql/go-cowsql/driver"
	"github.com/mattn/go-sqlite3"
)

// Populates error slices with Linux specific error types for use with SmartError().
func init() {
	httpResponseErrors[http.StatusConflict] = append(httpResponseErrors[http.StatusConflict], sqlite3.ErrConstraintUnique)
	httpResponseErrors[http.StatusServiceUnavailable] = append(httpResponseErrors[http.StatusServiceUnavailable], driver.ErrNoAvailableLeader)
}
