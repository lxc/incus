package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppArmorProfileName(t *testing.T) {
	for profile, expected := range map[string]string{
		"":                              "",
		"unconfined\n":                  "unconfined",
		"runc (unconfined)\n":           "unconfined",
		"docker-default (enforce)\n":    "docker-default (enforce)",
		"example (complain)\n":          "example (complain)",
		"first//&second (unconfined)\n": "unconfined",
		"confined//&runc (mixed)\n":     "confined//&runc (mixed)",
		"unconfined-name (enforce)\n":   "unconfined-name (enforce)",
	} {
		t.Run(profile, func(t *testing.T) {
			require.Equal(t, expected, appArmorProfileName(profile))
		})
	}
}
