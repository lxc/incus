package main

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	config "github.com/lxc/incus/v7/shared/cliconfig"
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
		{
			input:    []string{"incus", "--project", "default", "fizz", "c1", "buzz"},
			expected: []string{"incus", "--project", "default", "exec", "c1", "--", "echo", "buzz"},
		},
		{
			input:    []string{"incus", "--project=default", "fizz", "c1", "buzz"},
			expected: []string{"incus", "--project=default", "exec", "c1", "--", "echo", "buzz"},
		},
	}

	conf := &config.Config{Aliases: aliases}

	for _, tc := range testcases {
		app, _, _ := createApp()
		_ = app.ParseFlags(tc.input[1:])
		result, expanded, err := expandAlias(conf, tc.input, app)
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

func TestIsAdminCommand(t *testing.T) {
	rootCmd := &cobra.Command{Use: "incus"}
	adminCmd := &cobra.Command{Use: "admin"}
	osCmd := &cobra.Command{Use: "os"}
	infoCmd := &cobra.Command{Use: "info"}
	listCmd := &cobra.Command{Use: "list"}

	rootCmd.AddCommand(adminCmd, listCmd)
	adminCmd.AddCommand(osCmd)
	osCmd.AddCommand(infoCmd)

	assert.False(t, isAdminCommand(rootCmd))
	assert.False(t, isAdminCommand(listCmd))
	assert.True(t, isAdminCommand(adminCmd))
	assert.True(t, isAdminCommand(osCmd))
	assert.True(t, isAdminCommand(infoCmd))
}

func TestPersistentPreRunHooks(t *testing.T) {
	t.Setenv("INCUS_CONF", t.TempDir())

	app, _, err := createApp()
	require.NoError(t, err)

	cmd, _, err := app.Find([]string{"admin", "os", "info"})
	require.NoError(t, err)

	osCmd := cmd.Parent()
	require.Equal(t, "os", osCmd.Name())

	run := []string{}
	app.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		run = append(run, "root")
		return nil
	}

	app.PersistentPostRunE = nil
	osCmd.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		run = append(run, "os")
	}

	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		run = append(run, "command")
		return nil
	}

	app.SetArgs([]string{"admin", "os", "info"})
	app.SetOut(io.Discard)
	app.SetErr(io.Discard)

	err = app.Execute()
	require.NoError(t, err)
	assert.Equal(t, []string{"root", "os", "command"}, run)
}
