package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	config "github.com/cyphar/incus/internal/cliconfig"
)

type aliasTestcase struct {
	input     []string
	expected  []string
	expectErr bool
}

func slicesEqual(a, b []string) bool {
	if a == nil && b == nil {
		return true
	}

	if a == nil || b == nil {
		return false
	}

	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestExpandAliases(t *testing.T) {
	aliases := map[string]string{
		"tester 12":                "list",
		"foo":                      "list @ARGS@ -c n",
		"ssh":                      "/usr/bin/ssh @ARGS@",
		"bar":                      "exec c1 -- @ARGS@",
		"fizz":                     "exec @ARG1@ -- echo @ARG2@",
		"snaps":                    "query /1.0/instances/@ARG1@/snapshots",
		"snapshots with recursion": "query /1.0/instances/@ARG1@/snapshots?recursion=@ARG2@",
	}

	testcases := []aliasTestcase{
		{
			input:    []string{"lxc", "list"},
			expected: []string{"lxc", "list"},
		},
		{
			input:    []string{"lxc", "tester", "12"},
			expected: []string{"lxc", "list"},
		},
		{
			input:    []string{"lxc", "foo", "asdf"},
			expected: []string{"lxc", "list", "asdf", "-c", "n"},
		},
		{
			input:    []string{"lxc", "ssh", "c1"},
			expected: []string{"/usr/bin/ssh", "c1"},
		},
		{
			input:    []string{"lxc", "bar", "ls", "/"},
			expected: []string{"lxc", "exec", "c1", "--", "ls", "/"},
		},
		{
			input:    []string{"lxc", "fizz", "c1", "buzz"},
			expected: []string{"lxc", "exec", "c1", "--", "echo", "buzz"},
		},
		{
			input:     []string{"lxc", "fizz", "c1"},
			expectErr: true,
		},
		{
			input:    []string{"lxc", "snaps", "c1"},
			expected: []string{"lxc", "query", "/1.0/instances/c1/snapshots"},
		},
		{
			input:    []string{"lxc", "snapshots", "with", "recursion", "c1", "2"},
			expected: []string{"lxc", "query", "/1.0/instances/c1/snapshots?recursion=2"},
		},
	}

	conf := &config.Config{Aliases: aliases}

	for _, tc := range testcases {
		result, expanded, err := expandAlias(conf, tc.input)
		if tc.expectErr {
			assert.Error(t, err)
			continue
		}

		if !expanded {
			if !slicesEqual(tc.input, tc.expected) {
				t.Errorf("didn't expand when expected to: %s", tc.input)
			}

			continue
		}

		if !slicesEqual(result, tc.expected) {
			t.Errorf("%s didn't match %s", result, tc.expected)
		}
	}
}
