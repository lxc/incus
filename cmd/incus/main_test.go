package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	config "github.com/lxc/incus/v6/shared/cliconfig"
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
			input:    []string{"incus", "list"},
			expected: []string{"incus", "list"},
		},
		{
			input:    []string{"incus", "tester", "12"},
			expected: []string{"incus", "list"},
		},
		{
			input:    []string{"incus", "foo", "asdf"},
			expected: []string{"incus", "list", "asdf", "-c", "n"},
		},
		{
			input:    []string{"incus", "ssh", "c1"},
			expected: []string{"/usr/bin/ssh", "c1"},
		},
		{
			input:    []string{"incus", "bar", "ls", "/"},
			expected: []string{"incus", "exec", "c1", "--", "ls", "/"},
		},
		{
			input:    []string{"incus", "fizz", "c1", "buzz"},
			expected: []string{"incus", "exec", "c1", "--", "echo", "buzz"},
		},
		{
			input:     []string{"incus", "fizz", "c1"},
			expectErr: true,
		},
		{
			input:    []string{"incus", "snaps", "c1"},
			expected: []string{"incus", "query", "/1.0/instances/c1/snapshots"},
		},
		{
			input:    []string{"incus", "snapshots", "with", "recursion", "c1", "2"},
			expected: []string{"incus", "query", "/1.0/instances/c1/snapshots?recursion=2"},
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
