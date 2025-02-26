//go:build linux && cgo && !agent

package db

// Imports is a list of the package imports every generated source file has.
var Imports = []string{
	"database/sql",
	"fmt",
}
