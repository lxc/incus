package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/shared/api"
)

func TestPrepareImageServerFilters(t *testing.T) {
	filters := []string{"foo", "requirements.secureboot=false", "type=container"}

	result := prepareImageServerFilters(filters, api.InstanceFull{})
	assert.Equal(t, []string{"properties.requirements.secureboot=false", "type=container"}, result)
}
