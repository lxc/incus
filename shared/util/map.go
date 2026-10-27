package util

import (
	"maps"
)

// CloneMap returns a copy of m. This is a shallow clone: the new keys
// and values are set using ordinary assignment.
func CloneMap[M ~map[K]V, K comparable, V any](m M) M {
	if m == nil {
		return make(map[K]V)
	}

	return maps.Clone(m)
}
