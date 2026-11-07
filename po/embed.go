// Package po embeds the translation catalogues for builds that have no gettext
// to consult.
//
// The .po files stay in po/ as their source of truth, so translators and the
// gettext toolchain keep working unchanged. The pattern matches the catalogues
// and not incus.pot, which is a template and never read at runtime.
//
// Linux builds with cgo do not compile this package: they use GNU gettext and
// carry no embedded copy, leaving distributions free to ship and update
// translations on their own schedule.
package po

import (
	"embed"
)

// Files holds every catalogue embedded at build time.
//
//go:embed *.po
var Files embed.FS
