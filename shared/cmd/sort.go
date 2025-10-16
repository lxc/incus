package cmd

import (
	"strings"

	"github.com/fvbommel/sortorder"
)

// StringList represents the type for sorting nested string lists.
type StringList [][]string

func (a StringList) Len() int {
	return len(a)
}

func (a StringList) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a StringList) Less(i, j int) bool {
	x := 0
	for x = range a[i] {
		if a[i][x] != a[j][x] {
			break
		}
	}

	if a[i][x] == "" {
		return false
	}

	if a[j][x] == "" {
		return true
	}

	return sortorder.NaturalLess(a[i][x], a[j][x])
}

// SortColumnsNaturally represents the type for sorting columns in a natural order from left to right.
type SortColumnsNaturally [][]string

func (a SortColumnsNaturally) Len() int {
	return len(a)
}

func (a SortColumnsNaturally) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a SortColumnsNaturally) Less(i, j int) bool {
	for k := range a[i] {
		if a[i][k] == a[j][k] {
			continue
		}

		if a[i][k] == "" {
			return false
		}

		if a[j][k] == "" {
			return true
		}

		return sortorder.NaturalLess(a[i][k], a[j][k])
	}

	return false
}

// ByNameAndType represents the type for sorting Storage volumes.
type ByNameAndType [][]string

func (a ByNameAndType) Len() int {
	return len(a)
}

func (a ByNameAndType) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByNameAndType) Less(i, j int) bool {
	// Sort snapshot and parent together.
	iType := strings.Split(a[i][0], " ")[0]
	jType := strings.Split(a[j][0], " ")[0]

	if iType != jType {
		return sortorder.NaturalLess(a[i][0], a[j][0])
	}

	if a[i][1] == "" {
		return false
	}

	if a[j][1] == "" {
		return true
	}

	return sortorder.NaturalLess(a[i][1], a[j][1])
}
