//go:build cgo && !linux

package i18n

// G returns the translated string.
//
// A cgo build outside Linux has no gettext to call into, and this backend
// embeds nothing, so the msgid comes back unchanged. That is what these
// targets did before the cgo-free backend existed, and keeping it means a
// cgo build behaves identically on every platform.
//
// A build that wants the embedded catalogues sets CGO_ENABLED=0, which
// selects i18n_nocgo.go instead.
func G(msgid string) string {
	return msgid
}
