package cmd

import (
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/suite"
)

type sortSuite struct {
	suite.Suite
}

func TestSortSuite(t *testing.T) {
	suite.Run(t, new(sortSuite))
}

// stringList can be used to sort a list of strings.
func (s *sortSuite) Test_stringList() {
	data := [][]string{{"foo", "bar"}, {"baz", "bza"}}
	sort.Sort(StringList(data))
	s.Equal([][]string{{"baz", "bza"}, {"foo", "bar"}}, data)
}

// The first different string is used in sorting.
func (s *sortSuite) Test_stringList_sort_by_column() {
	data := [][]string{{"foo", "baz"}, {"foo", "bar"}}
	sort.Sort(StringList(data))
	s.Equal([][]string{{"foo", "bar"}, {"foo", "baz"}}, data)
}

// Empty strings are sorted last.
func (s *sortSuite) Test_stringList_empty_strings() {
	data := [][]string{{"", "bar"}, {"foo", "baz"}}
	sort.Sort(StringList(data))
	s.Equal([][]string{{"foo", "baz"}, {"", "bar"}}, data)
}

func (s *sortSuite) TestSortByPrecedence() {
	type args struct {
		data           [][]string
		displayColumns string
		sortColumns    string
	}

	tests := []struct {
		name      string
		args      args
		expect    [][]string
		expectErr error
	}{
		{
			name: "Sort column not in display columns",
			args: args{
				data: [][]string{
					{"b", "b", "c"},
					{"a", "b", "c"},
					{"c", "b", "a"},
				},
				displayColumns: "123",
				sortColumns:    "234",
			},
			expect: [][]string{
				{"b", "b", "c"},
				{"a", "b", "c"},
				{"c", "b", "a"},
			},
			expectErr: fmt.Errorf("Invalid sort column \"4\", not present in display columns \"123\""),
		},
		{
			name: "Sort column outside data range",
			args: args{
				data: [][]string{
					{"b", "b", "c", "d"},
					{"a", "b", "c", "d"},
					// Third row is shorter, so sort column "4" is outside the range.
					{"c", "b", "a"},
				},
				displayColumns: "1234",
				sortColumns:    "4",
			},
			expect: [][]string{
				{"b", "b", "c", "d"},
				{"a", "b", "c", "d"},
				{"c", "b", "a"},
			},
			expectErr: fmt.Errorf("Index of sort column \"4\" outside data range"),
		},
		{
			name: "Sort by first column",
			args: args{
				data: [][]string{
					{"b", "b", "c"},
					{"a", "b", "c"},
					{"c", "b", "a"},
				},
				displayColumns: "123",
				sortColumns:    "1",
			},
			expect: [][]string{
				{"a", "b", "c"},
				{"b", "b", "c"},
				{"c", "b", "a"},
			},
		},
		{
			name: "Sort by second column",
			args: args{
				data: [][]string{
					{"b", "b", "c"},
					{"a", "b", "c"},
					{"c", "b", "a"},
				},
				displayColumns: "123",
				sortColumns:    "2",
			},
			// Expect no change because column 2 is already in order
			expect: [][]string{
				{"b", "b", "c"},
				{"a", "b", "c"},
				{"c", "b", "a"},
			},
		},
		{
			name: "Sort by third column",
			args: args{
				data: [][]string{
					{"b", "b", "c"},
					{"a", "b", "c"},
					{"c", "b", "a"},
				},
				displayColumns: "123",
				sortColumns:    "3",
			},
			expect: [][]string{
				{"c", "b", "a"},
				{"b", "b", "c"},
				{"a", "b", "c"},
			},
		},
		{
			name: "Sort by third and first columns",
			args: args{
				data: [][]string{
					{"b", "b", "c"},
					{"a", "b", "c"},
					{"c", "b", "a"},
				},
				displayColumns: "123",
				sortColumns:    "31",
			},
			expect: [][]string{
				{"c", "b", "a"},
				{"a", "b", "c"},
				{"b", "b", "c"},
			},
		},
	}

	for i, test := range tests {
		s.T().Logf("Case %d: %s", i, test.name)

		err := SortByPrecedence(test.args.data, test.args.displayColumns, test.args.sortColumns)
		s.Equal(test.expectErr, err)
		s.Equal(test.expect, test.args.data)
	}
}
