package local

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/storage/s3"
)

// uploadInfo is persisted in upload.json under each in-flight upload directory.
type uploadInfo struct {
	Key         string            `json:"key"`
	ContentType string            `json:"content_type,omitempty"`
	UserMeta    map[string]string `json:"user_meta,omitempty"`
	Initiated   time.Time         `json:"initiated"`
}

// uploadsRoot opens the uploads directory as an os.Root, confining all
// uploadID-derived path operations and preventing path traversal.
func (s *Server) uploadsRoot() (*os.Root, error) {
	err := os.MkdirAll(s.uploadsDir(), 0o700)
	if err != nil {
		return nil, err
	}

	return os.OpenRoot(s.uploadsDir())
}

func (s *Server) initiateMultipartUpload(w http.ResponseWriter, r *http.Request, key string) {
	id := uuid.New().String()

	root, err := s.uploadsRoot()
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer func() { _ = root.Close() }()

	err = root.Mkdir(id, 0o700)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	info := &uploadInfo{
		Key:         key,
		ContentType: r.Header.Get("Content-Type"),
		UserMeta:    extractUserMeta(r.Header),
		Initiated:   time.Now().UTC(),
	}

	b, err := json.Marshal(info)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	err = root.WriteFile(filepath.Join(id, "upload.json"), b, 0o600)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	type initiateResult struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Bucket   string   `xml:"Bucket,omitempty"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}

	resp, err := xml.Marshal(&initiateResult{Key: key, UploadID: id})
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(resp)
}

func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request, key, uploadID string) {
	root, err := s.uploadsRoot()
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer func() { _ = root.Close() }()

	_, err = root.Stat(uploadID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Upload not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	partStr := r.URL.Query().Get("partNumber")
	partNumber, err := strconv.Atoi(partStr)
	if err != nil || partNumber < 1 || partNumber > 10000 {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid partNumber."}).Response(w)
		return
	}

	partPath := filepath.Join(uploadID, fmt.Sprintf("part-%05d", partNumber))
	tmp := partPath + ".tmp"

	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	hasher := md5.New()
	_, err = io.Copy(io.MultiWriter(f, hasher), r.Body)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = root.Remove(tmp)
		msg := err
		if msg == nil {
			msg = closeErr
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: msg.Error()}).Response(w)
		return
	}

	err = root.Rename(tmp, partPath)
	if err != nil {
		_ = root.Remove(tmp)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	etag := hex.EncodeToString(hasher.Sum(nil))
	w.Header().Set("ETag", `"`+etag+`"`)
	w.WriteHeader(http.StatusOK)
}

// completeRequest models the body of CompleteMultipartUpload.
type completeRequest struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []completePart `xml:"Part"`
}

type completePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (s *Server) completeMultipartUpload(w http.ResponseWriter, r *http.Request, key, uploadID string) {
	root, err := s.uploadsRoot()
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer func() { _ = root.Close() }()

	infoBytes, err := root.ReadFile(filepath.Join(uploadID, "upload.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Upload not found."}).Response(w)
			return
		}

		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	info := &uploadInfo{}
	err = json.Unmarshal(infoBytes, info)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	req := &completeRequest{}
	err = xml.Unmarshal(body, req)
	if err != nil {
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}).Response(w)
		return
	}

	// Sort by part number to assemble in order.
	sort.Slice(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })

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
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	combined := md5.New()
	var size int64
	for _, p := range req.Parts {
		partPath := filepath.Join(uploadID, fmt.Sprintf("part-%05d", p.PartNumber))
		f, err := root.Open(partPath)
		if err != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			(&s3.Error{Code: s3.ErrorInvalidRequest, Message: fmt.Sprintf("Missing part %d.", p.PartNumber)}).Response(w)
			return
		}

		n, copyErr := io.Copy(io.MultiWriter(out, combined), f)
		_ = f.Close()
		if copyErr != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			(&s3.Error{Code: s3.ErrorCodeInternalError, Message: copyErr.Error()}).Response(w)
			return
		}

		size += n
	}

	err = out.Close()
	if err != nil {
		_ = os.Remove(tmp)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	err = os.Rename(tmp, dataPath)
	if err != nil {
		_ = os.Remove(tmp)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	etag := hex.EncodeToString(combined.Sum(nil))
	meta := &objectMeta{
		ContentType: info.ContentType,
		ETag:        etag,
		Size:        size,
		LastMod:     time.Now().UTC(),
		UserMeta:    info.UserMeta,
	}

	err = writeMeta(metaPathFor(dataPath), meta)
	if err != nil {
		_ = os.Remove(dataPath)
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	// Clean up the upload directory.
	_ = root.RemoveAll(uploadID)

	type completeResult struct {
		XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
		Key     string   `xml:"Key"`
		ETag    string   `xml:"ETag"`
	}

	resp, err := xml.Marshal(&completeResult{Key: key, ETag: `"` + etag + `"`})
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(resp)
}

func (s *Server) abortMultipartUpload(w http.ResponseWriter, key, uploadID string) {
	root, err := s.uploadsRoot()
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	defer func() { _ = root.Close() }()

	err = root.RemoveAll(uploadID)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listMultipartUploads enumerates in-flight uploads. Minimal implementation:
// returns all uploads regardless of prefix/delimiter parameters.
func (s *Server) listMultipartUploads(w http.ResponseWriter, r *http.Request) {
	type upload struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
	}

	type result struct {
		XMLName xml.Name `xml:"ListMultipartUploadsResult"`
		Uploads []upload `xml:"Upload"`
	}

	out := &result{}

	entries, err := os.ReadDir(s.uploadsDir())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		b, err := os.ReadFile(filepath.Join(s.uploadsDir(), e.Name(), "upload.json"))
		if err != nil {
			continue
		}

		info := &uploadInfo{}
		if json.Unmarshal(b, info) != nil {
			continue
		}

		out.Uploads = append(out.Uploads, upload{
			Key:       info.Key,
			UploadID:  e.Name(),
			Initiated: info.Initiated.UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}

	body, err := xml.Marshal(out)
	if err != nil {
		(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(body)
}
