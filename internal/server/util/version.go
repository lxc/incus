package util

import (
	"errors"
)

// CompareVersions compares the versions of two cluster members.
//
// A version consists of the version the member's schema and the number of API
// extensions it supports.
//
// Return 0 if they equal, 1 if the first version is greater than the second
// and 2 if the second is greater than the first.
//
// Return an error if inconsistent versions are detected, for example the first
// member's schema is greater than the second's, but the number of extensions is
// smaller.
func CompareVersions(version1, version2 [2]int, checkExtensions bool) (int, error) {
	schema1, extensions1 := version1[0], version1[1]
	schema2, extensions2 := version2[0], version2[1]

	if !checkExtensions {
		// Don't compare API extensions.
		extensions1 = 0
		extensions2 = 0
	}

	if schema1 == schema2 && extensions1 == extensions2 {
		return 0, nil
	}

	if schema1 >= schema2 && extensions1 >= extensions2 {
		return 1, nil
	}

	if schema1 <= schema2 && extensions1 <= extensions2 {
		return 2, nil
	}

	return -1, errors.New("Cluster members have inconsistent versions")
}
