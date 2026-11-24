package cmd

import (
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

		rankI := sortRank(a[i][k])
		rankJ := sortRank(a[j][k])
		if rankI != rankJ {
			return rankI < rankJ
		}

		return sortorder.NaturalLess(a[i][k], a[j][k])
	}

	return false
}

// sortRank puts parenthesized cells after regular ones and empty cells last.
func sortRank(cell string) int {
	if cell == "" {
		return 2
	}

	if cell[0] == '(' {
		return 1
	}

	return 0
}
