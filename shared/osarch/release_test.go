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

func (s *releaseTestSuite) TestGetLSBRelease() {
	content := `NAME="Ubuntu"
ID="ubuntu"
VERSION_ID="16.04"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getLSBRelease(filename)
	s.Nil(err)
	s.Equal(
		map[string]string{
			"NAME":       "Ubuntu",
			"ID":         "ubuntu",
			"VERSION_ID": "16.04",
		}, lsbRelease)
}

func (s *releaseTestSuite) TestGetLSBReleaseSingleQuotes() {
	content := `NAME='Ubuntu'`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getLSBRelease(filename)
	s.Nil(err)
	s.Equal(map[string]string{"NAME": "Ubuntu"}, lsbRelease)
}

func (s *releaseTestSuite) TestGetLSBReleaseNoQuotes() {
	content := `NAME=Ubuntu`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getLSBRelease(filename)
	s.Nil(err)
	s.Equal(map[string]string{"NAME": "Ubuntu"}, lsbRelease)
}

func (s *releaseTestSuite) TestGetLSBReleaseSkipCommentsEmpty() {
	content := `
NAME="Ubuntu"

ID="ubuntu"
# skip this line
VERSION_ID="16.04"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	lsbRelease, err := getLSBRelease(filename)
	s.Nil(err)
	s.Equal(
		map[string]string{
			"NAME":       "Ubuntu",
			"ID":         "ubuntu",
			"VERSION_ID": "16.04",
		}, lsbRelease)
}

func (s *releaseTestSuite) TestGetLSBReleaseInvalidLine() {
	content := `
NAME="Ubuntu"
this is invalid
ID="ubuntu"
`
	filename, cleanup := WriteTempFile(&s.Suite, "", "os-release", content)
	defer cleanup()

	_, err := getLSBRelease(filename)
	s.EqualError(err, fmt.Sprintf("%s: invalid format on line 3", filename))
}
