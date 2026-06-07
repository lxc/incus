package local

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/v7/internal/server/storage/s3"
	"github.com/lxc/incus/v7/shared/logger"
)

func (s *Server) objectPath(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") {
		return "", errors.New("Invalid object key")
	}

	for _, seg := range strings.Split(key, "/") {
		if seg == ".." || seg == "." {
			return "", errors.New("Invalid object key")
		}
	}

	first, _, _ := strings.Cut(key, "/")
	if first == uploadsSubdir || strings.HasSuffix(key, metaSuffix) {
		return "", errors.New("Reserved object key")
	}

	return filepath.Join(s.dataDir(), key), nil
}

func (s *Server) headObject(w http.ResponseWriter, r *http.Request, key string) {
	dataPath, err := s.objectPath(key)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	meta, err := loadOrInferMeta(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorCodeNoSuchBucket, Message: "Object not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	writeObjectHeaders(w, meta)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request, key string) {
	dataPath, err := s.objectPath(key)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	meta, err := loadOrInferMeta(dataPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorCodeNoSuchBucket, Message: "Object not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	f, err := os.Open(dataPath)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer logger.WarnOnError(f.Close, "Failed to close file")

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		writeObjectHeaders(w, meta)
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}

	start, end, ok := parseSingleRange(rangeHeader, meta.Size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", meta.Size))
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid Range header."}).Response(w)
		return
	}

	_, err = f.Seek(start, io.SeekStart)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	length := end - start + 1
	writeObjectHeaders(w, meta)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, meta.Size))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.CopyN(w, f, length)
}

func (s *Server) putObject(w http.ResponseWriter, r *http.Request, key string) {
	dataPath, err := s.objectPath(key)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	err = os.MkdirAll(filepath.Dir(dataPath), 0o700)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	tmp := dataPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	hasher := md5.New()
	written, err := io.Copy(io.MultiWriter(f, hasher), r.Body)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(tmp)
		msg := err
		if msg == nil {
			msg = closeErr
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: msg.Error()}).Response(w)
		return
	}

	err = os.Rename(tmp, dataPath)
	if err != nil {
		_ = os.Remove(tmp)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	etag := hex.EncodeToString(hasher.Sum(nil))

	meta := &objectMeta{
		ContentType: r.Header.Get("Content-Type"),
		ETag:        etag,
		Size:        written,
		LastMod:     time.Now().UTC(),
		UserMeta:    extractUserMeta(r.Header),
	}

	err = writeMeta(metaPathFor(dataPath), meta)
	if err != nil {
		_ = os.Remove(dataPath)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

// copyObject handles a PUT carrying an X-Amz-Copy-Source header by copying
// the referenced source object's data and metadata onto the destination key.
//
// The metadata directive defaults to COPY, which preserves the source
// object's content-type and user metadata. REPLACE substitutes the values
// supplied on the request.
func (s *Server) copyObject(w http.ResponseWriter, r *http.Request, key string) {
	srcKey, ok := parseCopySource(r.Header.Get("X-Amz-Copy-Source"))
	if !ok {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid X-Amz-Copy-Source header."}).Response(w)
		return
	}

	srcPath, err := s.objectPath(srcKey)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	dstPath, err := s.objectPath(key)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	srcMeta, err := loadOrInferMeta(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorCodeNoSuchBucket, Message: "Source object not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	src, err := os.Open(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorCodeNoSuchBucket, Message: "Source object not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer logger.WarnOnError(src.Close, "Failed to close source file")

	err = os.MkdirAll(filepath.Dir(dstPath), 0o700)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	tmp := dstPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	hasher := md5.New()
	written, err := io.Copy(io.MultiWriter(f, hasher), src)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(tmp)
		msg := err
		if msg == nil {
			msg = closeErr
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: msg.Error()}).Response(w)
		return
	}

	err = os.Rename(tmp, dstPath)
	if err != nil {
		_ = os.Remove(tmp)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	contentType := srcMeta.ContentType
	userMeta := srcMeta.UserMeta
	if strings.EqualFold(r.Header.Get("X-Amz-Metadata-Directive"), "REPLACE") {
		contentType = r.Header.Get("Content-Type")
		userMeta = extractUserMeta(r.Header)
	}

	etag := hex.EncodeToString(hasher.Sum(nil))
	lastMod := time.Now().UTC()
	meta := &objectMeta{
		ContentType: contentType,
		ETag:        etag,
		Size:        written,
		LastMod:     lastMod,
		UserMeta:    userMeta,
	}

	err = writeMeta(metaPathFor(dstPath), meta)
	if err != nil {
		_ = os.Remove(dstPath)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	type copyResult struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		ETag         string   `xml:"ETag"`
		LastModified string   `xml:"LastModified"`
	}

	resp, err := xml.Marshal(&copyResult{
		ETag:         `"` + etag + `"`,
		LastModified: lastMod.Format("2006-01-02T15:04:05.000Z"),
	})
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(resp)
}

// parseCopySource extracts the source object key from an X-Amz-Copy-Source
// header value. The value has the form "[/]bucket/key" with the key
// optionally percent-encoded and an optional "?versionId=..." suffix.
func parseCopySource(v string) (string, bool) {
	if v == "" {
		return "", false
	}

	// Drop the optional version-id query suffix.
	idx := strings.Index(v, "?")
	if idx >= 0 {
		v = v[:idx]
	}

	decoded, err := url.PathUnescape(v)
	if err != nil {
		return "", false
	}

	decoded = strings.TrimPrefix(decoded, "/")

	_, key, ok := strings.Cut(decoded, "/")
	if !ok || key == "" {
		return "", false
	}

	return key, true
}

// handleObjectACL stubs the object-level ?acl sub-resource.
func (s *Server) handleObjectACL(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet:
		dataPath, err := s.objectPath(key)
		if err != nil {
			(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
			return
		}

		_, err = loadOrInferMeta(dataPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				(&s3.Error{Code: s3.ErrorCodeNoSuchBucket, Message: "Object not found."}).Response(w)
				return
			}

			(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
			return
		}

		const body = `<?xml version="1.0" encoding="UTF-8"?>` +
			`<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Owner><ID></ID><DisplayName></DisplayName></Owner>` +
			`<AccessControlList></AccessControlList>` +
			`</AccessControlPolicy>`

		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	case http.MethodPut:
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	default:
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Unsupported method for ?acl."}).Response(w)
	}
}

func (s *Server) deleteObject(w http.ResponseWriter, key string) {
	dataPath, err := s.objectPath(key)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	err = os.Remove(dataPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	err = removeMeta(metaPathFor(dataPath))
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeObjectHeaders(w http.ResponseWriter, meta *objectMeta) {
	if meta.ContentType != "" {
		w.Header().Set("Content-Type", meta.ContentType)
	}

	w.Header().Set("ETag", `"`+meta.ETag+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	w.Header().Set("Last-Modified", meta.LastMod.UTC().Format(http.TimeFormat))
	for k, v := range meta.UserMeta {
		w.Header().Set("X-Amz-Meta-"+k, v)
	}
}

func extractUserMeta(h http.Header) map[string]string {
	out := map[string]string{}
	for k, vs := range h {
		const prefix = "X-Amz-Meta-"
		if strings.HasPrefix(k, prefix) && len(vs) > 0 {
			out[strings.TrimPrefix(k, prefix)] = vs[0]
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// parseSingleRange parses a single byte-range header (the only form S3 requires). Returns inclusive start and end offsets.
func parseSingleRange(h string, size int64) (int64, int64, bool) {
	rest, ok := strings.CutPrefix(h, "bytes=")
	if !ok {
		return 0, 0, false
	}

	if strings.Contains(rest, ",") {
		return 0, 0, false
	}

	startStr, endStr, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, 0, false
	}

	if startStr == "" && endStr != "" {
		// Suffix range: "-N" means the last N bytes.
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 || n > size {
			n = size
		}

		return size - n, size - 1, true
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}

	end := size - 1
	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}

		if end >= size {
			end = size - 1
		}
	}

	return start, end, true
}
