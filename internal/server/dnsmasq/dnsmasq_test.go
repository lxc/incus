package dnsmasq

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_staticAllocationFileName(t *testing.T) {
	projectName := "test.project"
	instanceName := "test-instance"
	deviceName := "test/.-_--.device"
	fileName := StaticAllocationFileName(projectName, instanceName, deviceName)
	assert.Equal(t, "test.project_test-instance.test-.--_----.device", fileName)
}
