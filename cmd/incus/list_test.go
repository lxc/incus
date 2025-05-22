package main

import (
	"bytes"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/shared/api"
)

func TestShouldShow(t *testing.T) {
	list := cmdList{}
	inst := &api.Instance{
		Name: "foo",
		ExpandedConfig: map[string]string{
			"security.privileged": "1",
			"user.blah":           "abc",
			"image.os":            "Debian",
			"image.description":   "Debian buster amd64 (20200429_05:24)",
		},
		Status:   "Running",
		Location: "mem-brain",
		Type:     "Container",
		ExpandedDevices: map[string]map[string]string{
			"eth0": {
				"name":    "eth0",
				"type":    "nic",
				"parent":  "mybr0",
				"nictype": "bridged",
			},
		},
		InstancePut: api.InstancePut{
			Architecture: "potato",
			Description:  "Something which does something",
		},
	}

	state := &api.InstanceState{
		Network: map[string]api.InstanceStateNetwork{
			"eth0": {
				Addresses: []api.InstanceStateNetworkAddress{
					{
						Family:  "inet",
						Address: "10.29.85.156",
					},
					{
						Family:  "inet6",
						Address: "fd42:72a:89ac:e457:1266:6aff:fe83:8301",
					},
				},
			},
		},
	}

	if !list.shouldShow([]string{"ipv4=10.29.85.0/24"}, inst, state) {
		t.Errorf("net=10.29.85.0/24 filter didn't work")
	}

	if list.shouldShow([]string{"ipv4=10.29.85.0/32"}, inst, state) {
		t.Errorf("net=10.29.85.0/32 filter did work but should not")
	}

	if !list.shouldShow([]string{"ipv4=10.29.85.156"}, inst, state) {
		t.Errorf("net=10.29.85.156 filter did not work")
	}

	if !list.shouldShow([]string{"ipv6=fd42:72a:89ac:e457:1266:6aff:fe83:8301"}, inst, state) {
		t.Errorf("net=fd42:72a:89ac:e457:1266:6aff:fe83:8301 filter didn't work")
	}

	if list.shouldShow([]string{"ipv6=fd42:072a:89ac:e457:1266:6aff:fe83:ffff/128"}, inst, state) {
		t.Errorf("net=1net=fd42:072a:89ac:e457:1266:6aff:fe83:ffff/128 filter did work but should not")
	}

	if !list.shouldShow([]string{"ipv6=fd42:72a:89ac:e457:1266:6aff:fe83:ffff/1"}, inst, state) {
		t.Errorf("net=fd42:72a:89ac:e457:1266:6aff:fe83:ffff/1 filter filter didn't work")
	}
}

// Used by TestColumns and TestInvalidColumns.
const (
	shorthand = "46abcdDefFlmMnNpPsStuUL"
	alphanum  = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

func TestColumns(t *testing.T) {
	keys := make([]string, 0, len(instance.InstanceConfigKeysAny)+len(instance.InstanceConfigKeysContainer)+len(instance.InstanceConfigKeysVM))
	for k := range instance.InstanceConfigKeysAny {
		keys = append(keys, k)

		// Test compatibility with 'config:' prefix.
		keys = append(keys, "config:"+k)
	}

	for k := range instance.InstanceConfigKeysContainer {
		keys = append(keys, k)

		// Test compatibility with 'config:' prefix.
		keys = append(keys, "config:"+k)
	}

	for k := range instance.InstanceConfigKeysVM {
		keys = append(keys, k)

		// Test compatibility with 'config:' prefix.
		keys = append(keys, "config:"+k)
	}

	// Test with 'devices:'.
	keys = append(keys, "devices:eth0.parent.rand")
	keys = append(keys, "devices:root.path")

	randShorthand := func(buffer *bytes.Buffer) {
		buffer.WriteByte(shorthand[rand.Intn(len(shorthand))])
	}

	randString := func(buffer *bytes.Buffer) {
		l := rand.Intn(20)
		if l == 0 {
			l = rand.Intn(20) + 20
		}

		for range l {
			buffer.WriteByte(alphanum[rand.Intn(len(alphanum))])
		}
	}

	randConfigKey := func(buffer *bytes.Buffer) {
		// Unconditionally prepend a comma so that we don't create an invalid
		// column string, redundant commas will be handled immediately prior
		// to parsing the string.
		buffer.WriteRune(',')

		switch rand.Intn(4) {
		case 0:
			buffer.WriteString(keys[rand.Intn(len(keys))])
		case 1:
			buffer.WriteString("user.")
			randString(buffer)
		case 2:
			buffer.WriteString("environment.")
			randString(buffer)
		case 3:
			if rand.Intn(2) == 0 {
				buffer.WriteString(instance.ConfigVolatilePrefix)
				randString(buffer)
				buffer.WriteString(".hwaddr")
			} else {
				buffer.WriteString(instance.ConfigVolatilePrefix)
				randString(buffer)
				buffer.WriteString(".name")
			}
		}

		// Randomize the optional fields in a single shot.  Empty names are legal
		// when specifying the max width, append an extra colon in this case.
		opt := rand.Intn(8)
		if opt&1 != 0 {
			buffer.WriteString(":")
			randString(buffer)
		} else if opt != 0 {
			buffer.WriteString(":")
		}

		switch opt {
		case 2, 3:
			buffer.WriteString(":-1")
		case 4, 5:
			buffer.WriteString(":0")
		case 6, 7:
			buffer.WriteRune(':')
			buffer.WriteString(strconv.FormatUint(uint64(rand.Uint32()), 10))
		}

		// Unconditionally append a comma so that we don't create an invalid
		// column string, redundant commas will be handled immediately prior
		// to parsing the string.
		buffer.WriteRune(',')
	}

	for range 1000 {
		go func() {
			var buffer bytes.Buffer

			l := rand.Intn(10)
			if l == 0 {
				l = rand.Intn(10) + 10
			}

			num := l
			for range l {
				switch rand.Intn(5) {
				case 0:
					if buffer.Len() > 0 {
						buffer.WriteRune(',')
						num--
					} else {
						randShorthand(&buffer)
					}

				case 1, 2:
					randShorthand(&buffer)
				case 3, 4:
					randConfigKey(&buffer)
				}
			}

			// Generate the column string, removing any leading, trailing or duplicate commas.
			raw := removeDuplicatesFromString(strings.Trim(buffer.String(), ","), ",")

			list := cmdList{flagColumns: raw}

			clustered := strings.Contains(raw, "L")
			columns, _, err := list.parseColumns(clustered)
			if err != nil {
				t.Errorf("Failed to parse columns string.  Input: %s, Error: %s", raw, err)
			}

			if len(columns) != num {
				t.Errorf("Did not generate correct number of columns.  Expected: %d, Actual: %d, Input: %s", num, len(columns), raw)
			}
		}()
	}
}

func removeDuplicatesFromString(s string, sep string) string {
	dup := sep + sep

	for strings.Contains(s, dup) {
		s = strings.ReplaceAll(s, dup, sep)
	}

	return s
}

func TestInvalidColumns(t *testing.T) {
	run := func(raw string) {
		list := cmdList{flagColumns: raw}
		_, _, err := list.parseColumns(true)
		if err == nil {
			t.Errorf("Expected error from parseColumns, received nil.  Input: %s", raw)
		}
	}

	for _, v := range alphanum {
		if !strings.ContainsRune(shorthand, v) {
			run(string(v))
		}
	}

	run(",")
	run(",a")
	run("a,")
	run("4,,6")
	run(".")
	run(":")
	run("::")
	run(".key:")
	run("user.key:")
	run("user.key::")
	run(":user.key")
	run(":user.key:0")
	run("user.key::-2")
	run("user.key:name:-2")
	run("volatile")
	run("base_image")
	run("volatile.image")
	run("config:")
	run("config:image")
	run("devices:eth0")
}

func TestPrepareInstanceServerFilters(t *testing.T) {
	filters := []string{"foo", "user.a=blah", "name=v1", "state=running"}

	result := prepareInstanceServerFilters(filters, api.InstanceFull{})
	assert.Equal(t, []string{"name=(^foo$|^foo.*)", "expanded_config.user.a=blah", "name=v1", "status=running"}, result)
}
