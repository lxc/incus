package util

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"

	"github.com/lxc/incus/v6/shared/cancel"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/units"
)

// ErrNotFound is used to explicitly signal error cases, where a resource
// can not be found (404 HTTP status code).
var ErrNotFound = errors.New("resource not found")

// DownloadFileHash downloads a file while validating its hash.
func DownloadFileHash(ctx context.Context, httpClient *http.Client, useragent string, progress func(progress ioprogress.ProgressData), canceler *cancel.HTTPRequestCanceller, filename string, url string, fileHash string, hashFunc hash.Hash, target io.WriteSeeker) (int64, error) {
	// Always seek to the beginning
	_, _ = target.Seek(0, io.SeekStart)

	var req *http.Request
	var err error

	// Prepare the download request
	if ctx != nil {
		req, err = http.NewRequestWithContext(ctx, "GET", url, nil)
	} else {
		req, err = http.NewRequest("GET", url, nil)
	}

	if err != nil {
		return -1, err
	}

	if useragent != "" {
		req.Header.Set("User-Agent", useragent)
	}

	// Perform the request
	r, doneCh, err := cancel.CancelableDownload(canceler, httpClient.Do, req)
	if err != nil {
		return -1, err
	}

	defer func() { _ = r.Body.Close() }()
	defer close(doneCh)

	if r.StatusCode != http.StatusOK {
		if r.StatusCode == http.StatusNotFound {
			return -1, fmt.Errorf("Unable to fetch %s: %w", url, ErrNotFound)
		}

		return -1, fmt.Errorf("Unable to fetch %s: %s", url, r.Status)
	}

	// Handle the data
	body := r.Body
	if progress != nil {
		body = &ioprogress.ProgressReader{
			ReadCloser: r.Body,
			Tracker: &ioprogress.ProgressTracker{
				Length: r.ContentLength,
				Handler: func(percent int64, speed int64) {
					if filename != "" {
						progress(ioprogress.ProgressData{Text: fmt.Sprintf("%s: %d%% (%s/s)", filename, percent, units.GetByteSizeString(speed, 2))})
					} else {
						progress(ioprogress.ProgressData{Text: fmt.Sprintf("%d%% (%s/s)", percent, units.GetByteSizeString(speed, 2))})
					}
				},
			},
		}
	}

	var size int64

	if hashFunc != nil {
		size, err = io.Copy(io.MultiWriter(target, hashFunc), body)
		if err != nil {
			return -1, err
		}

		result := fmt.Sprintf("%x", hashFunc.Sum(nil))
		if result != fileHash {
			return -1, fmt.Errorf("Hash mismatch for %s: %s != %s", url, result, fileHash)
		}
	} else {
		size, err = io.Copy(target, body)
		if err != nil {
			return -1, err
		}
	}

	return size, nil
}
