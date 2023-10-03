package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test the alphabetical sorting of a `incus_doc` YAML structure.
func TestYAMLSorted(t *testing.T) {
	projectEntries := map[string][]any{
		"groupKey1": {
			map[string]any{
				"b.core.server.test.a": map[string]string{
					"todo1": "stuff",
					"todo2": "stuff",
				},
			},
			map[string]any{
				"a.core.server.test.c": map[string]string{
					"todo3": "stuff",
					"todo4": "stuff",
				},
			},
			map[string]any{
				"a.core.server.test.b": map[string]string{
					"todo5": "stuff",
					"todo6": "stuff",
				},
			},
		},
		"groupKey2": {
			map[string]any{
				"aaa.ccc.bbb": map[string]string{
					"todo7": "stuff",
					"todo8": "stuff",
				},
			},
			map[string]any{
				"000.111.222": map[string]string{
					"todo9":  "stuff",
					"todo10": "stuff",
				},
			},
			map[string]any{
				"zzz.*": map[string]string{
					"todo11": "stuff",
					"todo12": "stuff",
				},
			},
		},
	}

	sortedProjectEntries := map[string][]any{
		"groupKey1": {
			map[string]any{
				"a.core.server.test.b": map[string]string{
					"todo5": "stuff",
					"todo6": "stuff",
				},
			},
			map[string]any{
				"a.core.server.test.c": map[string]string{
					"todo3": "stuff",
					"todo4": "stuff",
				},
			},
			map[string]any{
				"b.core.server.test.a": map[string]string{
					"todo1": "stuff",
					"todo2": "stuff",
				},
			},
		},
		"groupKey2": {
			map[string]any{
				"000.111.222": map[string]string{
					"todo9":  "stuff",
					"todo10": "stuff",
				},
			},
			map[string]any{
				"aaa.ccc.bbb": map[string]string{
					"todo7": "stuff",
					"todo8": "stuff",
				},
			},
			map[string]any{
				"zzz.*": map[string]string{
					"todo11": "stuff",
					"todo12": "stuff",
				},
			},
		},
	}

	assert.Equal(t, sortedProjectEntries, sortConfigKeys(projectEntries))
}
