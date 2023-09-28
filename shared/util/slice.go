package util

// ValueInSlice returns true if key is in list.
func ValueInSlice[T comparable](key T, list []T) bool {
	for _, entry := range list {
		if entry == key {
			return true
		}
	}

	return false
}
