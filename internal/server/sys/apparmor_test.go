//go:build linux && cgo && !agent

package sys

import (
	"os"
	"testing"
)

func TestHaveMacAdmin(t *testing.T) {
	macAdmin := haveMacAdmin()

	uid := os.Getuid()
	t.Log(macAdmin, uid)

	if macAdmin != (uid == 0) {
		t.Fatal(uid, macAdmin)
	}
}
