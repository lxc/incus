//go:build linux && cgo && !agent

package db

// Imports is a list of the package imports every generated source file has.
var Imports = []string{
	"context",
	"database/sql",
	"fmt",
	"strings",
	"github.com/mattn/go-sqlite3",
}
