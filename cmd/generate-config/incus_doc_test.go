package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test the alphabetical sorting of a `incus-doc` JSON structure.
func TestJSONSorted(t *testing.T) {
	projectEntries := make(map[string]any)
	projectEntries["entityKey1"] = map[string]any{
		"groupKey1": map[string]any{
			"keys": []any{
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
		},
	}

	projectEntries["entityKey2"] = map[string]any{
		"groupKey2": map[string]any{
			"keys": []any{
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
		},
	}

	sortedProjectEntries := make(map[string]any)
	sortedProjectEntries["entityKey1"] = map[string]any{
		"groupKey1": map[string]any{
			"keys": []any{
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
		},
	}

	sortedProjectEntries["entityKey2"] = map[string]any{
		"groupKey2": map[string]any{
			"keys": []any{
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
		},
	}

	sortConfigKeys(projectEntries)
	assert.Equal(t, sortedProjectEntries, projectEntries)
}
