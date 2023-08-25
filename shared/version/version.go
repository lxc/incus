package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// DottedVersion holds element of a version in the maj.min[.patch] format.
type DottedVersion struct {
	Major int
	Minor int
	Patch int
}

// NewDottedVersion returns a new Version.
func NewDottedVersion(versionString string) (*DottedVersion, error) {
	formatError := fmt.Errorf("Invalid version format: %q", versionString)
	split := strings.Split(versionString, ".")
	if len(split) < 2 {
		return nil, formatError
	}

	maj, err := strconv.Atoi(split[0])
	if err != nil {
		return nil, formatError
	}

	min, err := strconv.Atoi(split[1])
	if err != nil {
		return nil, formatError
	}

	patch := -1
	if len(split) == 3 {
		patch, err = strconv.Atoi(split[2])
		if err != nil {
			return nil, formatError
		}
	}

	return &DottedVersion{
		Major: maj,
		Minor: min,
		Patch: patch,
	}, nil
}

// Parse parses a string starting with a dotted version and returns it.
func Parse(s string) (*DottedVersion, error) {
	r, err := regexp.Compile(`^([0-9]+.[0-9]+(?:.[0-9]+)?)`)
	if err != nil {
		return nil, err
	}

	matches := r.FindStringSubmatch(s)
	if len(matches) == 0 {
		return nil, fmt.Errorf("Can't parse a version: %s", s)
	}

	return NewDottedVersion(matches[1])
}

// String returns version as a string.
func (v *DottedVersion) String() string {
	version := fmt.Sprintf("%d.%d", v.Major, v.Minor)
	if v.Patch != -1 {
		version += fmt.Sprintf(".%d", v.Patch)
	}

	return version
}

// Compare returns result of comparison between two versions.
func (v *DottedVersion) Compare(other *DottedVersion) int {
	result := compareInts(v.Major, other.Major)
	if result != 0 {
		return result
	}

	result = compareInts(v.Minor, other.Minor)
	if result != 0 {
		return result
	}

	return compareInts(v.Patch, other.Patch)
}

func compareInts(i1 int, i2 int) int {
	switch {
	case i1 < i2:
		return -1
	case i1 > i2:
		return 1
	default:
		return 0
	}
}
