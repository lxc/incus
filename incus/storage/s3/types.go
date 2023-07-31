package s3

import (
	"encoding/xml"
	"net/http"
	"time"
)

// ErrorCodeNoSuchBucket means the specified bucket does not exist.
const ErrorCodeNoSuchBucket = "NoSuchBucket"

// ErrorCodeInternalError means there was an internal error.
const ErrorCodeInternalError = "InternalError"

// ErrorCodeInvalidAccessKeyID means there was an invalid access key provided.
const ErrorCodeInvalidAccessKeyID = "InvalidAccessKeyId"

// ErrorInvalidRequest means there was an invalid request.
const ErrorInvalidRequest = "InvalidRequest"

var errorHTTPStatusCodes = map[string]int{
	ErrorCodeNoSuchBucket:       http.StatusNotFound,
	ErrorCodeInternalError:      http.StatusInternalServerError,
	ErrorCodeInvalidAccessKeyID: http.StatusForbidden,
	ErrorInvalidRequest:         http.StatusBadRequest,
}

// Error S3 error response.
type Error struct {
	Code       string
	Message    string
	Resource   string
	RequestID  string `xml:"RequestId"`
	BucketName string `xml:"BucketName,omitempty"`
	HostID     string `xml:"HostId"`
}

// Response writes error as HTTP response.
func (r *Error) Response(w http.ResponseWriter) {
	resp, err := xml.Marshal(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/xml")

	statusCode := errorHTTPStatusCodes[r.Code]
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}

	w.WriteHeader(statusCode)

	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(resp)
}

// Owner S3 owner.
type Owner struct {
	ID          string
	DisplayName string
}

// Bucket S3 bucket.
type Bucket struct {
	CreationDate time.Time
	Name         string
}

// ListAllMyBucketsResult S3 list my buckets.
type ListAllMyBucketsResult struct {
	Owner   Owner
	Buckets []Bucket `xml:"Buckets>Bucket"`
}

// Response writes error as HTTP response.
func (r *ListAllMyBucketsResult) Response(w http.ResponseWriter) {
	resp, err := xml.Marshal(r)
	if err != nil {
		errResult := Error{Code: ErrorCodeInternalError, Message: err.Error()}
		errResult.Response(w)

		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	_, _ = w.Write(resp)
}
