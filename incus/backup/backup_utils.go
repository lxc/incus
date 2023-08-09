package backup

import (
	"archive/tar"
	"context"
	"fmt"
	"io"

	"github.com/lxc/incus/incus/archive"
	"github.com/lxc/incus/incus/sys"
	"github.com/lxc/incus/shared"
)

// TarReader rewinds backup file handle r and returns new tar reader and process cleanup function.
func TarReader(r io.ReadSeeker, sysOS *sys.OS, outputPath string) (*tar.Reader, context.CancelFunc, error) {
	_, err := r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, nil, err
	}

	_, _, unpacker, err := shared.DetectCompressionFile(r)
	if err != nil {
		return nil, nil, err
	}

	if unpacker == nil {
		return nil, nil, fmt.Errorf("Unsupported backup compression")
	}

	tr, cancelFunc, err := archive.CompressedTarReader(context.Background(), r, unpacker, sysOS, outputPath)
	if err != nil {
		return nil, nil, err
	}

	return tr, cancelFunc, nil
}
