package instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHashName(t *testing.T) {
	value := HashName("test", 5)
	assert.Equal(t, "test", value)

	value = HashName("test1", 5)
	assert.Equal(t, "test1", value)

	value = HashName("test12", 5)
	assert.Equal(t, "qY7Fx", value)

	value = HashName("test12345", 11)
	assert.Equal(t, "test12345", value)

	value = HashName("test12345678", 11)
	assert.Equal(t, "9fvG_oTDZTF", value)
}
