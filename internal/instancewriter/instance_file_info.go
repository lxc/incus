package instancewriter

import (
	"archive/tar"
	"os"
	"time"
)

// FileInfo static file implementation of os.FileInfo.
type FileInfo struct {
	FileName    string
	FileSize    int64
	FileMode    os.FileMode
	FileModTime time.Time
}

// Name of file.
func (f *FileInfo) Name() string {
	return f.FileName
}

// Size of file.
func (f *FileInfo) Size() int64 {
	return f.FileSize
}

// Mode of file.
func (f *FileInfo) Mode() os.FileMode {
	return f.FileMode
}

// ModTime of file.
func (f *FileInfo) ModTime() time.Time {
	return f.FileModTime
}

// IsDir is file a directory.
func (f *FileInfo) IsDir() bool {
	return false
}

// Sys returns further unix attributes for a file owned by root.
func (f *FileInfo) Sys() any {
	return &tar.Header{
		Uid:        0,
		Gid:        0,
		Uname:      "root",
		Gname:      "root",
		AccessTime: time.Now(),
		ChangeTime: time.Now(),
	}
}
