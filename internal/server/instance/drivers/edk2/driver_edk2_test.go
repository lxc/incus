//go:build amd64 || arm64

package edk2

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lxc/incus/v6/internal/server/util"
)

func tSetup(t *testing.T) func() {
	t.Helper()
	tmpdir, err := os.MkdirTemp("", "edk2*")
	if err != nil {
		t.Fatal(err)
	}

	incusEdk2Path := filepath.Join(tmpdir, "ovmf")
	err = os.MkdirAll(incusEdk2Path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	for _, fn := range []string{"OVMF_CODE.fd", "OVMF.fd", "OVMF_VARS.fd"} {
		f, err := os.Create(filepath.Join(incusEdk2Path, fn))
		if err != nil {
			t.Fatal(err)
		}

		err = f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	err = os.Setenv("INCUS_EDK2_PATH", incusEdk2Path)
	if err != nil {
		t.Fatal(err)
	}

	return func() {
		err = os.Unsetenv("INCUS_EDK2_PATH")
		if err != nil {
			t.Fatal(err)
		}

		err = os.RemoveAll(tmpdir)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestGetEdk2Path(t *testing.T) {
	incusEDK2Path, err := GetenvEdk2Path()
	if err != nil || incusEDK2Path != "" {
		t.Fatal(err, incusEDK2Path)
	}

	rb := tSetup(t)
	defer rb()
	incusEDK2Path, err = GetenvEdk2Path()
	if err != nil || incusEDK2Path == "" {
		t.Fatal(incusEDK2Path)
	}
}

func TestArchitectureFirmwarePairs(t *testing.T) {
	rb := tSetup(t)
	defer rb()
	architectures, err := util.GetArchitectures()
	if err != nil || len(architectures) == 0 {
		t.Fatal(err)
	}

	pairs, err := GetArchitectureFirmwarePairs(architectures[0])
	if err != nil {
		t.Fatal(err)
	}

	if len(pairs) == 0 {
		t.Fatal(pairs)
	}

	pair1 := pairs[0]
	code, vars := pair1.Code, pair1.Vars
	if !strings.HasSuffix(code, "ovmf/OVMF_CODE.fd") ||
		!strings.HasSuffix(vars, "ovmf/OVMF_VARS.fd") {
		t.Fatal(pair1)
	}
}
