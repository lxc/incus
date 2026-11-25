// Package po embeds the compact translation catalogs for builds without gettext.
package po

import (
	"embed"
)

// Files holds the catalogs written by generate-catalogs, only a placeholder when it didn't run before the build.
//
//go:embed all:catalogs
var Files embed.FS
