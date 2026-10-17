package local

import (
	"encoding/xml"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/internal/server/storage/s3"
)

// listObjectsResult is the XML root for ListObjects and ListObjectsV2 responses.
type listObjectsResult struct {
	XMLName               xml.Name              `xml:"ListBucketResult"`
	Name                  string                `xml:"Name,omitempty"`
	Prefix                string                `xml:"Prefix"`
	Delimiter             string                `xml:"Delimiter,omitempty"`
	MaxKeys               int                   `xml:"MaxKeys"`
	KeyCount              *int                  `xml:"KeyCount,omitempty"`
	IsTruncated           bool                  `xml:"IsTruncated"`
	Marker                string                `xml:"Marker,omitempty"`
	NextMarker            string                `xml:"NextMarker,omitempty"`
	NextContinuationToken string                `xml:"NextContinuationToken,omitempty"`
	StartAfter            string                `xml:"StartAfter,omitempty"`
	Contents              []listObjectsV2Object `xml:"Contents"`
	CommonPrefixes        []listCommonPrefix    `xml:"CommonPrefixes"`
}

type listObjectsV2Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type listCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// listObjects implements ListObjects and ListObjectsV2.
func (s *Server) listObjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	startAfter := q.Get("start-after")
	listV2 := q.Get("list-type") == "2"

	token := q.Get("continuation-token")
	if token != "" {
		startAfter = token
	}

	if !listV2 {
		startAfter = q.Get("marker")
	}

	maxKeys := 1000

	v := q.Get("max-keys")
	if v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 && n < 1000 {
			maxKeys = n
		}
	}

	keys, err := s.collectKeys()
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	sort.Strings(keys)

	result := &listObjectsResult{
		Name:       s.bucketName,
		Prefix:     prefix,
		Delimiter:  delimiter,
		MaxKeys:    maxKeys,
		StartAfter: q.Get("start-after"),
	}

	if !listV2 {
		result.Marker = startAfter
		result.StartAfter = ""
	}

	// Last key returned or folded into a common prefix, where the next page starts after.
	lastKey := ""
	keyCount := 0
	seenPrefix := map[string]bool{}

	for _, k := range keys {
		if startAfter != "" && k <= startAfter {
			continue
		}

		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}

		if delimiter != "" {
			rest := strings.TrimPrefix(k, prefix)

			idx := strings.Index(rest, delimiter)
			if idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if !seenPrefix[cp] {
					seenPrefix[cp] = true
					if keyCount >= maxKeys {
						result.IsTruncated = true
						break
					}

					result.CommonPrefixes = append(result.CommonPrefixes, listCommonPrefix{Prefix: cp})
					keyCount++
				}

				lastKey = k
				continue
			}
		}

		if keyCount >= maxKeys {
			result.IsTruncated = true
			break
		}

		lastKey = k

		dataPath := filepath.Join(s.dataDir(), k)
		meta, err := loadOrInferMeta(dataPath)
		if err != nil {
			// Data file vanished between walk and stat.
			continue
		}

		result.Contents = append(result.Contents, listObjectsV2Object{
			Key:          k,
			LastModified: meta.LastMod.UTC().Format("2006-01-02T15:04:05.000Z"),
			ETag:         `"` + meta.ETag + `"`,
			Size:         meta.Size,
			StorageClass: "STANDARD",
		})

		keyCount++
	}

	// KeyCount only exists in ListObjectsV2.
	if listV2 {
		result.KeyCount = &keyCount
	}

	if result.IsTruncated {
		if listV2 {
			result.NextContinuationToken = lastKey
		} else {
			result.NextMarker = lastKey
		}
	}

	body, err := xml.Marshal(result)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(body)
}

// collectKeys walks the data directory and returns the list of object keys.
// Sidecar files, the uploads directory, and temporary files are skipped.
func (s *Server) collectKeys() ([]string, error) {
	root := s.dataDir()
	keys := []string{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return filepath.SkipAll
			}

			return err
		}

		if path == root {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if d.IsDir() {
			if rel == uploadsSubdir {
				return filepath.SkipDir
			}

			return nil
		}

		// Skip metadata, in-flight temporary files, and dotfiles.
		base := filepath.Base(rel)
		if strings.HasSuffix(base, metaSuffix) || strings.HasSuffix(base, ".tmp") {
			return nil
		}

		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	return keys, nil
}
