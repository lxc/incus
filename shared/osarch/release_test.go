package osarch

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"
)

type releaseTestSuite struct {
	suite.Suite
}

func TestReleaseTestSuite(t *testing.T) {
	suite.Run(t, new(releaseTestSuite))
}

func (s *releaseTestSuite) TestGetOSRelease() {
	content := `NAME="Ubuntu"
ID="ubuntu"
VERSION_ID="16.04"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getOSRelease(filename)
	s.Nil(err)
	s.Equal(
		map[string]string{
			"NAME":       "Ubuntu",
			"ID":         "ubuntu",
			"VERSION_ID": "16.04",
		}, lsbRelease)
}

func (s *releaseTestSuite) TestGetOSReleaseSingleQuotes() {
	content := `NAME='Ubuntu'`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getOSRelease(filename)
	s.Nil(err)
	s.Equal(map[string]string{"NAME": "Ubuntu"}, lsbRelease)
}

func (s *releaseTestSuite) TestGetOSReleaseNoQuotes() {
	content := `NAME=Ubuntu`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getOSRelease(filename)
	s.Nil(err)
	s.Equal(map[string]string{"NAME": "Ubuntu"}, lsbRelease)
}

func (s *releaseTestSuite) TestGetOSReleaseSkipCommentsEmpty() {
	content := `
NAME="Ubuntu"

ID="ubuntu"
# skip this line
VERSION_ID="16.04"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getOSRelease(filename)
	s.Nil(err)
	s.Equal(
		map[string]string{
			"NAME":       "Ubuntu",
			"ID":         "ubuntu",
			"VERSION_ID": "16.04",
		}, lsbRelease)
}

func (s *releaseTestSuite) TestGetOSReleaseInvalidLine() {
	content := `
NAME="Ubuntu"
this is invalid
ID="ubuntu"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	_, err := getOSRelease(filename)
	s.EqualError(err, fmt.Sprintf("%s: invalid format on line 3", filename))
}
