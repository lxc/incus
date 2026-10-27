package main

import (
	"io/fs"
	"reflect"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
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
	s.Equal(structHasField(reflect.TypeFor[api.Image](), "type"), true)
	s.Equal(structHasField(reflect.TypeFor[api.Image](), "public"), true)
	s.Equal(structHasField(reflect.TypeFor[api.Image](), "foo"), false)
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

func (s *utilsTestSuite) TestParseMode() {
	// Test this in parallel in a shell with:
	// grep -oP '^\t// > \K.*$' cmd/incus/utils_test.go | sh
	mode1741, _ := util.ParseMode("1741")
	// > set -eu
	// > file="test-$(uuidgen)"
	// > touch "$file"
	// > check() {
	// >   umask "$3"
	// >   chmod "$2" "$file"
	// >   chmod "$1" "$file" 2>/dev/null || true
	// >   got="$(stat -c '%04a' "$file")"
	// >   [ "$got" = "$4" ] || (echo "Expected $4, got $got"; false)
	// > }
	mode, _ := parseMode("1741", 0, 0, false)
	s.Equal(mode1741, mode)
	// > check 1741 0 0 1741
	mode, _ = parseMode("=", 0o771, 0, false)
	s.Equal(fs.FileMode(0), mode)
	// > check = 771 0 0000
	mode, _ = parseMode("=", 0o771, 0o022, false)
	s.Equal(fs.FileMode(0), mode)
	// > check = 771 022 0000
	mode, _ = parseMode("=rwx", 0o771, 0, false)
	s.Equal(fs.FileMode(0o777), mode)
	// > check =rwx 771 0 0777
	mode, _ = parseMode("=rwx", 0o771, 0o022, false)
	s.Equal(fs.FileMode(0o755), mode)
	// > check =rwx 771 022 0755
	mode, _ = parseMode("+rwx", 0o771, 0o022, false)
	s.Equal(fs.FileMode(0o775), mode)
	// > check +rwx 771 022 0775
	mode, _ = parseMode("-rwx", 0o771, 0o022, false)
	s.Equal(fs.FileMode(0o020), mode)
	// > check -rwx 771 022 0020
	mode, _ = parseMode("a-rwx", 0o771, 0o022, false)
	s.Equal(fs.FileMode(0), mode)
	// > check a-rwx 771 022 0000
	mode, _ = parseMode("o+t,a+r,o-r,go-w,g-x", 0o333, 0, false)
	s.Equal(mode1741, mode)
	// > check o+t,a+r,o-r,go-w,g-x 333 0 1741
	mode, _ = parseMode("ug-x,a+X", 0o770, 0, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check ug-x,a+X 770 0 0660
	mode, _ = parseMode("ug-x,+X", 0o770, 0, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check ug-x,+X 770 0 0660
	mode, _ = parseMode("ug-x,a+X", 0o770, 0o011, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check ug-x,a+X 770 011 0660
	mode, _ = parseMode("ug-x,+X", 0o770, 0o011, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check ug-x,+X 770 011 0660
	mode, _ = parseMode("-x,a+X", 0o770, 0, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check -x,a+X 770 0 0660
	mode, _ = parseMode("-x,+X", 0o770, 0, false)
	s.Equal(fs.FileMode(0o660), mode)
	// > check -x,+X 770 0 0660
	mode, _ = parseMode("-x,a+X", 0o770, 0o011, false)
	s.Equal(fs.FileMode(0o771), mode)
	// > check -x,a+X 770 011 0771
	mode, _ = parseMode("-x,+X", 0o770, 0o011, false)
	s.Equal(fs.FileMode(0o770), mode)
	// > check -x,+X 770 011 0770
	mode, _ = parseMode("a+X,ug-x", 0o770, 0, false)
	s.Equal(fs.FileMode(0o661), mode)
	// > check a+X,ug-x 770 0 0661
	mode, _ = parseMode("ug-x,a+X", 0o770, 0, true)
	s.Equal(fs.FileMode(0o771), mode)
	// > rm "$file" && mkdir "$file"
	// > check ug-x,a+X 770 0 0771
	mode, _ = parseMode("a+X,ug-x", 0o770, 0, true)
	s.Equal(fs.FileMode(0o661), mode)
	// > check a+X,ug-x 770 0 0661
	mode, _ = parseMode("+X", 0o640, 0o010, true)
	s.Equal(fs.FileMode(0o741), mode)
	// > check +X 640 010 0741
	mode, _ = parseMode("u=o", 0o751, 0o010, true)
	s.Equal(fs.FileMode(0o151), mode)
	// > check u=o 751 010 0151
	mode, _ = parseMode("u=o,o=u", 0o751, 0o010, true)
	s.Equal(fs.FileMode(0o151), mode)
	// > check u=o,o=u 751 010 0151
	mode, _ = parseMode("g=o,o=u,u=g,g=rx", 0o751, 0o010, true)
	s.Equal(fs.FileMode(0o157), mode)
	// > check g=o,o=u,u=g,g=rx 751 010 0157
	mode, _ = parseMode("=u", 0o751, 0o023, true)
	s.Equal(fs.FileMode(0o754), mode)
	// > check =u 751 023 0754
	mode, _ = parseMode("ug-o", 0o751, 0o010, true)
	s.Equal(fs.FileMode(0o641), mode)
	// > check ug-o 751 010 0641
	mode, _ = parseMode("-o", 0o751, 0o010, true)
	s.Equal(fs.FileMode(0o650), mode)
	// > check -o 751 010 0650
	mode, _ = parseMode("ug+o", 0o641, 0o010, true)
	s.Equal(fs.FileMode(0o751), mode)
	// > check ug+o 641 010 0751
	mode, _ = parseMode("+o", 0o641, 0o010, true)
	s.Equal(fs.FileMode(0o741), mode)
	// > check +o 641 010 0741
	mode, _ = parseMode("-777", 0o777, 0o023, true)
	s.Equal(fs.FileMode(0), mode)
	// > check -777 777 023 0000
	mode, _ = parseMode("+777,-111", 0o321, 0o023, true)
	s.Equal(fs.FileMode(0o666), mode)
	// > check +777,-111 321 023 0666
	// > rmdir "$file"
	// > echo OK
}
