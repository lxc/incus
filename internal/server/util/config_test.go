package util_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	localUtil "github.com/lxc/incus/v6/internal/server/util"
)

func Test_CompareConfigsMismatch(t *testing.T) {
	cases := []struct {
		config1 map[string]string
		config2 map[string]string
		error   string
	}{
		{
			map[string]string{"foo": "bar"},
			map[string]string{"foo": "egg"},
			"different values for keys: foo",
		},
		{
			map[string]string{"foo": "bar"},
			map[string]string{"egg": "buz"},
			"different values for keys: egg, foo",
		},
	}

	for _, c := range cases {
		t.Run(c.error, func(t *testing.T) {
			err := localUtil.CompareConfigs(c.config1, c.config2, nil)
			assert.EqualError(t, err, c.error)
		})
	}
}

func Test_CompareConfigs(t *testing.T) {
	config1 := map[string]string{"foo": "bar", "baz": "buz"}
	config2 := map[string]string{"foo": "egg", "baz": "buz"}
	err := localUtil.CompareConfigs(config1, config2, []string{"foo"})
	assert.NoError(t, err)
}
