package cmd

import (
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
