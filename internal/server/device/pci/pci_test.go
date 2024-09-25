package pci_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/server/device/pci"
)

func TestNormaliseAddress(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"0000:00:00.0": "0000:00:00.0",
		"1000:00:00.0": "1000:00:00.0",
		"00:00.0":      "0000:00:00.0",
		"0000:AB:00.0": "0000:ab:00.0",
		"1000:AB:00.0": "1000:ab:00.0",
		"00:AB.0":      "0000:00:ab.0",
	}

	for k, v := range cases {
		res := pci.NormaliseAddress(k)

		assert.Equal(t, res, v)
	}
}
