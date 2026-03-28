package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/incus/v6/shared/api"
)

type utilsTestSuite struct {
	suite.Suite
}

func TestUtilsTestSuite(t *testing.T) {
	suite.Run(t, &utilsTestSuite{})
}

func (s *utilsTestSuite) TestisAliasesSubsetTrue() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	s.Exactly(isAliasesSubset(a1, a2), true)
}

func (s *utilsTestSuite) TestisAliasesSubsetFalse() {
	a1 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "bar"},
	}

	a2 := []api.ImageAlias{
		{Name: "foo"},
		{Name: "baz"},
	}

	s.Exactly(isAliasesSubset(a1, a2), false)
}

func (s *utilsTestSuite) TestgetExistingAliases() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := getExistingAliases([]string{"bar", "foo", "other"}, images)
	s.Exactly([]api.ImageAliasesEntry{images[0], images[1]}, aliases)
}

func (s *utilsTestSuite) TestgetExistingAliasesEmpty() {
	images := []api.ImageAliasesEntry{
		{Name: "foo"},
		{Name: "bar"},
		{Name: "baz"},
	}

	aliases := getExistingAliases([]string{"other1", "other2"}, images)
	s.Exactly([]api.ImageAliasesEntry{}, aliases)
}

func (s *utilsTestSuite) TestStructHasFields() {
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "type"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "public"), true)
	s.Equal(structHasField(reflect.TypeOf(api.Image{}), "foo"), false)
}

func (s *utilsTestSuite) TestGetServerSupportedFilters() {
	filters := []string{
		"foo", "type=container", "user.blah=a", "status=running,stopped",
	}

	supportedFilters, unsupportedFilters := getServerSupportedFilters(filters, []string{}, false)
	s.Equal([]string{"type=container", "user.blah=a", "status=running,stopped"}, supportedFilters)
	s.Equal([]string{"foo"}, unsupportedFilters)

	supportedFilters, unsupportedFilters = getServerSupportedFilters(filters, []string{}, true)
	s.Equal([]string{"foo", "type=container", "user.blah=a", "status=running,stopped"}, supportedFilters)
	s.Equal([]string{}, unsupportedFilters)

	supportedFilters, unsupportedFilters = getServerSupportedFilters(filters, []string{"type", "status"}, true)
	s.Equal([]string{"foo", "user.blah=a"}, supportedFilters)
	s.Equal([]string{"type=container", "status=running,stopped"}, unsupportedFilters)
}
