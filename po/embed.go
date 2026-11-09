// Package po embeds the translation catalogues for builds that have no gettext
// to consult.
//
// The .po files stay in po/ as their source of truth, so translators and the
// gettext toolchain keep working unchanged. The pattern matches the catalogues
// and not incus.pot, which is a template and never read at runtime.
//
// A build with cgo does not compile this package, so it carries no embedded
// copy: on Linux the translations come from GNU gettext and distributions stay
// free to ship and update them on their own schedule; elsewhere such a build
// returns the msgid, as it did before embedding existed.
//
// A build without cgo always embeds, on every platform, because it has no
// gettext to consult.
package po

import (
	"embed"
)

// Files holds every catalogue embedded at build time.
//
//go:embed *.po
var Files embed.FS
