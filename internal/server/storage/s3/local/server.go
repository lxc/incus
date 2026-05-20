// Package local implements an in-process S3-compatible HTTP handler that
// serves objects from a directory on the local filesystem.
//
// It is used by Incus to expose buckets backed by local storage drivers
// (dir, btrfs, zfs) without spawning an external S3 server.
//
// On-disk layout under the bucket directory:
//
//	data/<key>           object data
//	data/<key>.meta      object metadata (JSON)
//	data/.uploads/<id>/  in-flight multipart upload state
package local

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/lxc/incus/v7/internal/server/storage/s3"
)

const (
	dataSubdir    = "data"
	uploadsSubdir = ".uploads"
)

// Role describes what operations a Credential is permitted to perform.
type Role string

const (
	// RoleAdmin allows all S3 operations on the bucket.
	RoleAdmin Role = "admin"

	// RoleReadOnly allows only read operations (GET/HEAD on objects and bucket listing).
	RoleReadOnly Role = "read-only"
)

// Credential is an S3 access-key / secret-key pair authorised against the bucket.
type Credential struct {
	AccessKey string
	SecretKey string
	Role      Role
}

// Server serves S3 requests for a single bucket directory.
type Server struct {
	bucketDir string
	creds     []Credential

	// OnAuthenticated, if set, is invoked once the request has been
	// authenticated and authorised, before any data on disk is touched.
	// Errors are returned to the client as an internal-error response and
	// dispatch is aborted.
	OnAuthenticated func() error
}

// NewServer returns a Server rooted at bucketDir.
//
// bucketDir is the per-bucket directory on the local filesystem. Object data
// is read from and written to bucketDir/data/. The directory is created on
// demand for object writes.
//
// creds lists the access-key / secret-key pairs that can authenticate against
// the bucket. The first matching access key is used for SigV4 verification.
func NewServer(bucketDir string, creds []Credential) *Server {
	return &Server{
		bucketDir: bucketDir,
		creds:     creds,
	}
}

func (s *Server) dataDir() string {
	return filepath.Join(s.bucketDir, dataSubdir)
}

func (s *Server) uploadsDir() string {
	return filepath.Join(s.dataDir(), uploadsSubdir)
}

// ServeHTTP implements http.Handler.
//
// The bucket name component of the URL is ignored: this handler is scoped to
// a single bucket and the caller is expected to have routed by bucket name
// already. Routing happens on the remainder of the path.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Authenticate the request before any I/O.
	role, authErr := s.authenticate(r)
	if authErr != nil {
		authErr.Response(w)
		return
	}

	objectKey := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(objectKey, "/", 2)
	if len(parts) == 2 {
		objectKey = parts[1]
	} else {
		objectKey = ""
	}

	if !methodAllowedForRole(r.Method, role, objectKey, r.URL.Query()) {
		(&s3.Error{
			Code:    s3.ErrorInvalidRequest,
			Message: "Operation not permitted by credential role.",
		}).Response(w)
		return
	}

	if s.OnAuthenticated != nil {
		err := s.OnAuthenticated()
		if err != nil {
			(&s3.Error{Code: s3.ErrorCodeInternalError, Message: err.Error()}).Response(w)
			return
		}
	}

	if objectKey == "" {
		s.handleBucket(w, r)
		return
	}

	s.handleObject(w, r, objectKey)
}

func methodAllowedForRole(method string, role Role, objectKey string, q url.Values) bool {
	// Admin access.
	if role == RoleAdmin {
		return true
	}

	// Unknown role.
	if role != RoleReadOnly {
		return false
	}

	// Read-only is limited to GET and HEAD on objects, and GET on the
	// bucket for listing. Multipart sub-resources are writes.
	_, ok := q["uploads"]
	if ok {
		return false
	}

	if q.Get("uploadId") != "" {
		return false
	}

	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	}

	return false
}

func (s *Server) handleBucket(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// ListObjectsV2 (and a few other listings keyed off query parameters).
		_, ok := r.URL.Query()["uploads"]
		if ok {
			s.listMultipartUploads(w, r)
			return
		}

		s.listObjects(w, r)
	case http.MethodHead:
		// Bucket exist if we made it this far.
		w.WriteHeader(http.StatusOK)
	default:
		// We don't allow bucket creation/deletion.
		(&s3.Error{
			Code:    s3.ErrorInvalidRequest,
			Message: "Bucket lifecycle is managed by the Incus API.",
		}).Response(w)
	}
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, objectKey string) {
	q := r.URL.Query()
	_, ok := q["uploads"]
	if ok && r.Method == http.MethodPost {
		s.initiateMultipartUpload(w, r, objectKey)
		return
	}

	uploadID := q.Get("uploadId")
	if uploadID != "" {
		switch r.Method {
		case http.MethodPut:
			s.uploadPart(w, r, objectKey, uploadID)
		case http.MethodPost:
			s.completeMultipartUpload(w, r, objectKey, uploadID)
		case http.MethodDelete:
			s.abortMultipartUpload(w, objectKey, uploadID)
		default:
			(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Unsupported method for multipart upload."}).Response(w)
		}

		return
	}

	// We don't actually support ACLs but still will return an empty one to avoid confusing clients.
	_, ok = q["acl"]
	if ok {
		s.handleObjectACL(w, r, objectKey)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.getObject(w, r, objectKey)
	case http.MethodHead:
		s.headObject(w, r, objectKey)
	case http.MethodPut:
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			s.copyObject(w, r, objectKey)
			return
		}

		s.putObject(w, r, objectKey)
	case http.MethodDelete:
		s.deleteObject(w, objectKey)
	default:
		(&s3.Error{Code: s3.ErrorInvalidRequest, Message: "Unsupported method."}).Response(w)
	}
}
