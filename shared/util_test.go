package shared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestURLEncode(t *testing.T) {
	url, _ := URLEncode(
		"/path/with spaces",
		map[string]string{"param": "with spaces", "other": "without"})
	expected := "/path/with%20spaces?other=without&param=with+spaces"
	if url != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", url, expected))
	}
}

func TestUrlsJoin(t *testing.T) {
	baseUrl := "http://images.linuxcontainers.org/streams/v1/"
	path := "../../image/root.tar.xz"

	res, err := JoinUrls(baseUrl, path)
	if err != nil {
		t.Error(err)
		return
	}

	expected := "http://images.linuxcontainers.org/image/root.tar.xz"
	if res != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", res, expected))
	}
}

func TestFileCopy(t *testing.T) {
	helloWorld := []byte("hello world\n")
	source, err := os.CreateTemp("", "")
	if err != nil {
		t.Error(err)
		return
	}

	defer func() { _ = os.Remove(source.Name()) }()

	err = WriteAll(source, helloWorld)
	if err != nil {
		_ = source.Close()
		t.Error(err)
		return
	}

	_ = source.Close()

	dest, err := os.CreateTemp("", "")
	defer func() { _ = os.Remove(dest.Name()) }()
	if err != nil {
		t.Error(err)
		return
	}

	_ = dest.Close()

	err = FileCopy(source.Name(), dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	dest2, err := os.Open(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	content, err := io.ReadAll(dest2)
	if err != nil {
		t.Error(err)
		return
	}

	if string(content) != string(helloWorld) {
		t.Error("content mismatch: ", string(content), "!=", string(helloWorld))
		return
	}
}

func TestDirCopy(t *testing.T) {
	dir, err := os.MkdirTemp("", "incus-shared-util-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()

	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "dest")

	dir1 := "dir1"
	dir2 := "dir2"

	file1 := "file1"
	file2 := "dir1/file1"

	content1 := []byte("file1")
	content2 := []byte("file2")

	require.NoError(t, os.Mkdir(source, 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir1), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir2), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, file1), content1, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(source, file2), content2, 0755))

	require.NoError(t, DirCopy(source, dest))

	for _, path := range []string{dir1, dir2, file1, file2} {
		assert.True(t, PathExists(filepath.Join(dest, path)))
	}

	bytes, err := os.ReadFile(filepath.Join(dest, file1))
	require.NoError(t, err)
	assert.Equal(t, content1, bytes)

	bytes, err = os.ReadFile(filepath.Join(dest, file2))
	require.NoError(t, err)
	assert.Equal(t, content2, bytes)
}

func TestGetExpiry(t *testing.T) {
	refDate := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiryDate, err := GetExpiry(refDate, "1M 2H 3d 4w 5m 6y")
	expectedDate := time.Date(2006, time.July, 2, 2, 1, 0, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	refDate = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	expiryDate, err = GetExpiry(refDate, "5S 1M 2H 3d 4y")
	expectedDate = time.Date(2004, time.January, 4, 2, 1, 5, 0, time.UTC)
	require.NoError(t, err)
	require.Equal(t, expectedDate, expiryDate)

	expiryDate, err = GetExpiry(refDate, "0M 0H 0d 0w 0m 0y")
	require.NoError(t, err)
	require.Equal(t, expiryDate, expiryDate)

	expiryDate, err = GetExpiry(refDate, "")
	require.NoError(t, err)
	require.Equal(t, time.Time{}, expiryDate)

	expiryDate, err = GetExpiry(refDate, "1z")
	require.Error(t, err)
	require.Equal(t, time.Time{}, expiryDate)
}

func TestHasKey(t *testing.T) {
	m1 := map[string]string{
		"foo":   "bar",
		"empty": "",
	}

	m2 := map[int]string{
		1: "foo",
	}

	assert.True(t, HasKey("foo", m1))
	assert.True(t, HasKey("empty", m1))
	assert.False(t, HasKey("missing", m1))

	assert.True(t, HasKey(1, m2))
	assert.False(t, HasKey(0, m2))
}

func TestRemoveElementsFromStringSlice(t *testing.T) {
	type test struct {
		elementsToRemove []string
		list             []string
		expectedList     []string
	}

	tests := []test{
		{
			elementsToRemove: []string{"one", "two", "three"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"four", "five"},
		},
		{
			elementsToRemove: []string{"two", "three", "four"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"one", "five"},
		},
		{
			elementsToRemove: []string{"two", "three", "four"},
			list:             []string{"two", "three"},
			expectedList:     []string{},
		},
		{
			elementsToRemove: []string{"two", "two", "two"},
			list:             []string{"two"},
			expectedList:     []string{},
		},
		{
			elementsToRemove: []string{"two", "two", "two"},
			list:             []string{"one", "two", "three", "four", "five"},
			expectedList:     []string{"one", "three", "four", "five"},
		},
	}

	for _, tt := range tests {
		gotList := RemoveElementsFromStringSlice(tt.list, tt.elementsToRemove...)
		assert.ElementsMatch(t, tt.expectedList, gotList)
	}
}
