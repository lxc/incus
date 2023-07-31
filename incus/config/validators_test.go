package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cyphar/incus/lxd/config"
)

func TestAvailableExecutable(t *testing.T) {
	assert.NoError(t, config.AvailableExecutable("ls"))
	assert.NoError(t, config.AvailableExecutable("none"))
	assert.Error(t, config.AvailableExecutable("somenonexistingbin"))
}
