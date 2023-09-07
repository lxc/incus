package cmd

import (
	"fmt"
	"sort"
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

// SortByPrecedence sorts the given data in order of precedence. Each row of the data argument should have length equal
// to len(displayColumns). The sortColumns argument should be a subset of displayColumns.
func SortByPrecedence(data [][]string, displayColumns string, sortColumns string) error {
	precedence := make([]int, len(sortColumns))
	for i, r := range sortColumns {
		index := strings.IndexRune(displayColumns, r)
		if index < 0 {
			return fmt.Errorf("Invalid sort column %q, not present in display columns %q", string(r), displayColumns)
		}

		for _, row := range data {
			if index >= len(row) {
				return fmt.Errorf("Index of sort column %q outside data range", string(r))
			}
		}

		precedence[i] = index
	}

	sort.Sort(byPrecedence{
		precedence: precedence,
		data:       data,
	})

	return nil
}

// byPrecedence implements the sort.Interface. It is intentionally private because it may panic. Use the
// SortByPrecedence method instead.
type byPrecedence struct {
	// precedence defines the column indexes to be sorted in order of precedence.
	precedence []int
	// data is the table data to be sorted.
	data [][]string
}

func (a byPrecedence) Len() int {
	if a.data == nil {
		return 0
	}

	return len(a.data)
}

func (a byPrecedence) Swap(i, j int) {
	if a.data == nil {
		return
	}

	a.data[i], a.data[j] = a.data[j], a.data[i]
}

func (a byPrecedence) Less(i, j int) bool {
	for _, k := range a.precedence {
		if k >= len(a.data[i]) {
			panic("Given precedence index is out of bounds")
		}

		if k >= len(a.data[j]) {
			panic("Given precedence index is out of bounds")
		}

		if a.data[i][k] == a.data[j][k] {
			continue
		}

		if a.data[i][k] == "" {
			return false
		}

		if a.data[j][k] == "" {
			return true
		}

		return sortorder.NaturalLess(a.data[i][k], a.data[j][k])
	}

	return false
}
