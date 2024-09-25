package version

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type versionTestSuite struct {
	suite.Suite
}

func TestVersionTestSuite(t *testing.T) {
	suite.Run(t, new(versionTestSuite))
}

func (s *versionTestSuite) TestNewVersion() {
	v, err := NewDottedVersion("1.2.3")
	s.Nil(err)
	s.Equal(1, v.Major)
	s.Equal(2, v.Minor)
	s.Equal(3, v.Patch)
}

func (s *versionTestSuite) TestNewVersionNoPatch() {
	v, err := NewDottedVersion("1.2")
	s.Nil(err)
	s.Equal(-1, v.Patch)
}

func (s *versionTestSuite) TestNewVersionInvalid() {
	v, err := NewDottedVersion("1.nope")
	s.Nil(v)
	s.NotNil(err)
}

func (s *versionTestSuite) TestParseDashes() {
	v, err := Parse("1.2.3-asdf")
	s.Nil(err)
	s.Equal(1, v.Major)
	s.Equal(2, v.Minor)
	s.Equal(3, v.Patch)
}

func (s *versionTestSuite) TestParseParentheses() {
	v, err := Parse("1.2.3(beta1)")
	s.Nil(err)
	s.Equal(1, v.Major)
	s.Equal(2, v.Minor)
	s.Equal(3, v.Patch)
}

func (s *versionTestSuite) TestParseFail() {
	v, err := Parse("asdfaf")
	s.Nil(v)
	s.NotNil(err)
}

func (s *versionTestSuite) TestString() {
	v, _ := NewDottedVersion("1.2.3")
	s.Equal("1.2.3", v.String())
}

func (s *versionTestSuite) TestCompareEqual() {
	v1, _ := NewDottedVersion("1.2.3")
	v2, _ := NewDottedVersion("1.2.3")
	s.Equal(0, v1.Compare(v2))
	s.Equal(0, v2.Compare(v1))
	v3, _ := NewDottedVersion("1.2")
	v4, _ := NewDottedVersion("1.2")
	s.Equal(0, v3.Compare(v4))
	s.Equal(0, v4.Compare(v3))
}

func (s *versionTestSuite) TestCompareOlder() {
	v1, _ := NewDottedVersion("1.2.3")
	v2, _ := NewDottedVersion("1.2.4")
	v3, _ := NewDottedVersion("1.3")
	v4, _ := NewDottedVersion("2.2.3")
	s.Equal(-1, v1.Compare(v2))
	s.Equal(-1, v1.Compare(v3))
	s.Equal(-1, v1.Compare(v4))
}

func (s *versionTestSuite) TestCompareNewer() {
	v1, _ := NewDottedVersion("1.2.3")
	v2, _ := NewDottedVersion("1.2.2")
	v3, _ := NewDottedVersion("1.1")
	v4, _ := NewDottedVersion("0.3.3")
	s.Equal(1, v1.Compare(v2))
	s.Equal(1, v1.Compare(v3))
	s.Equal(1, v1.Compare(v4))
}
