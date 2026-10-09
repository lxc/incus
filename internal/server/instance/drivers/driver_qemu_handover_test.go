package drivers

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/shared/api"
)

// A paused source whose guest already runs on its live migration target must never resume.
func TestQEMUUnfreezeRefusesUnacknowledgedHandover(t *testing.T) {
	t.Setenv("INCUS_DIR", t.TempDir())
	d := &qemu{common: common{name: "vm", project: api.Project{Name: "default"}}}
	require.NoError(t, os.MkdirAll(d.RunPath(), 0o700))
	require.NoError(t, os.WriteFile(d.handoverUnacknowledgedPath(), []byte("target\n"), 0o600))

	err := d.Unfreeze()
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), err)
}
