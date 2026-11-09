//go:build cgo && !linux

package i18n

import (
	"testing"
)

// TestGReturnsMsgidWithoutGettext pins what a cgo build outside Linux does.
// There is no libintl to call and nothing embedded, so G must return its
// argument unchanged rather than panicking or reaching for a catalogue.
//
// The build tag keeps this file out of the Linux tree, where the same
// configuration compiles i18n_linux.go instead. It runs on a macOS or
// FreeBSD machine with cgo enabled, which is where the branch matters.
func TestGReturnsMsgidWithoutGettext(t *testing.T) {
	for _, msgid := range []string{
		"Description:",
		"",
		"multi\nline",
		`quote " inside`,
	} {
		got := G(msgid)
		if got != msgid {
			t.Errorf("G(%q) = %q, want the msgid back", msgid, got)
		}
	}
}
