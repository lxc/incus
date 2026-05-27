package local

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/v7/internal/server/storage/s3"
)

// Hardcoded body hash sentinels used by AWS clients.
const (
	unsignedPayload          = "UNSIGNED-PAYLOAD"
	streamingPayload         = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	streamingPayloadTrailer  = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	streamingUnsignedTrailer = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"
)

// authenticate verifies the SigV4 signature on the request and returns the
// matching credential's role on success, or an *s3.Error response on failure.
//
// On success, r.Body is replaced with a buffered copy if the body's hash had
// to be computed for verification. The caller must use r.Body, not the
// original.
func (s *Server) authenticate(r *http.Request) (Role, *s3.Error) {
	query := r.URL.Query()

	// Handle pre-signed SigV4.
	if query.Get("X-Amz-Signature") != "" {
		return s.authenticatePresignedV4(r)
	}

	// Handle pre-signed SigV2.
	if query.Get("AWSAccessKeyId") != "" || query.Get("Signature") != "" {
		return s.authenticatePresignedV2(r)
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Missing Authorization header."}
	}

	accessKey := s3.AuthorizationHeaderAccessKey(authHeader)
	if accessKey == "" {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Could not extract access key."}
	}

	secret, role, found := s.lookupCredential(accessKey)
	if !found {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Unknown access key."}
	}

	parsed, err := parseAuthorizationHeader(authHeader)
	if err != nil {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}
	}

	parsed.amzDate = r.Header.Get("X-Amz-Date")
	if parsed.amzDate == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing X-Amz-Date header."}
	}

	// Resolve the body hash for the canonical request.
	bodyHash := r.Header.Get("X-Amz-Content-Sha256")
	streaming := false

	switch bodyHash {
	case streamingPayload, streamingPayloadTrailer, streamingUnsignedTrailer:
		streaming = true

	case "", unsignedPayload:
		// Use the header value as-is for the canonical request.

	default:
		// A signed body hash was provided. The client claims the body
		// hashes to bodyHash; we must verify that. Buffer the body, hash
		// it, compare.
		if r.Body != nil {
			buf, readErr := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if readErr != nil {
				return "", &s3.Error{Code: s3.ErrorCodeInternalError, Message: "Failed to read request body."}
			}

			actual := sha256Hex(buf)
			if actual != bodyHash {
				return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Body hash mismatch."}
			}

			r.Body = io.NopCloser(bytes.NewReader(buf))
		}
	}

	if bodyHash == "" {
		bodyHash = unsignedPayload
	}

	canonical := canonicalRequest(r, r.URL.Query(), parsed.signedHeaders, bodyHash)

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		parsed.amzDate,
		parsed.scope,
		sha256Hex([]byte(canonical)),
	}, "\n")

	signingKey := deriveSigningKey(secret, parsed.scopeDate, parsed.scopeRegion, parsed.scopeService)
	expected := hmacSHA256Hex(signingKey, stringToSign)

	if !hmac.Equal([]byte(expected), []byte(parsed.signature)) {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Signature mismatch."}
	}

	if streaming && r.Body != nil && hasAWSChunkedEncoding(r) {
		err := wrapStreamingBody(r, bodyHash, parsed, signingKey)
		if err != nil {
			return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: err.Error()}
		}
	}

	return role, nil
}

func (s *Server) lookupCredential(accessKey string) (string, Role, bool) {
	for _, c := range s.creds {
		if c.AccessKey == accessKey {
			return c.SecretKey, c.Role, true
		}
	}

	return "", "", false
}

// Handle pre-signed SigV4 request validation.
func (s *Server) authenticatePresignedV4(r *http.Request) (Role, *s3.Error) {
	q := r.URL.Query()

	algorithm := q.Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Unsupported presigned signature algorithm."}
	}

	credential := q.Get("X-Amz-Credential")
	if credential == "" {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Missing X-Amz-Credential."}
	}

	// <accessKey>/<date>/<region>/<service>/aws4_request
	//
	// Access keys may contain "/" so do a reverse split.
	fields := strings.Split(credential, "/")
	if len(fields) < 5 {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Malformed X-Amz-Credential."}
	}

	accessKey := strings.Join(fields[:len(fields)-4], "/")
	scopeDate := fields[len(fields)-4]
	scopeRegion := fields[len(fields)-3]
	scopeService := fields[len(fields)-2]
	scope := strings.Join(fields[len(fields)-4:], "/")

	secret, role, found := s.lookupCredential(accessKey)
	if !found {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Unknown access key."}
	}

	amzDate := q.Get("X-Amz-Date")
	if amzDate == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing X-Amz-Date."}
	}

	signedAt, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid X-Amz-Date."}
	}

	expiresStr := q.Get("X-Amz-Expires")
	if expiresStr == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing X-Amz-Expires."}
	}

	expires, err := strconv.Atoi(expiresStr)
	if err != nil || expires <= 0 {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid X-Amz-Expires."}
	}

	if time.Duration(expires)*time.Second > 7*24*time.Hour {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "X-Amz-Expires exceeds the maximum of 7 days."}
	}

	if time.Now().UTC().After(signedAt.Add(time.Duration(expires) * time.Second)) {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Presigned URL has expired."}
	}

	signedHeaders := strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	if len(signedHeaders) == 0 || signedHeaders[0] == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing X-Amz-SignedHeaders."}
	}

	sort.Strings(signedHeaders)

	// The canonical query string covers every query parameter except the
	// signature itself.
	canonicalQuery := make(url.Values, len(q))
	for k, v := range q {
		if k == "X-Amz-Signature" {
			continue
		}

		canonicalQuery[k] = v
	}

	canonical := canonicalRequest(r, canonicalQuery, signedHeaders, unsignedPayload)

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")

	signingKey := deriveSigningKey(secret, scopeDate, scopeRegion, scopeService)
	expected := hmacSHA256Hex(signingKey, stringToSign)

	if !hmac.Equal([]byte(expected), []byte(q.Get("X-Amz-Signature"))) {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Signature mismatch."}
	}

	return role, nil
}

var presignedV2ResourceSubresources = []string{
	"response-content-disposition",
	"response-content-type",
}

// Handle pre-signed SigV2 request validation.
func (s *Server) authenticatePresignedV2(r *http.Request) (Role, *s3.Error) {
	q := r.URL.Query()

	accessKey := q.Get("AWSAccessKeyId")
	if accessKey == "" {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Missing AWSAccessKeyId."}
	}

	secret, role, found := s.lookupCredential(accessKey)
	if !found {
		return "", &s3.Error{Code: s3.ErrorCodeInvalidAccessKeyID, Message: "Unknown access key."}
	}

	providedSignature := q.Get("Signature")
	if providedSignature == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing Signature."}
	}

	expiresStr := q.Get("Expires")
	if expiresStr == "" {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Missing Expires."}
	}

	// Expires is an absolute Unix timestamp at which the URL stops being valid.
	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Invalid Expires."}
	}

	if time.Now().Unix() > expires {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Presigned URL has expired."}
	}

	// Build the canonical resource: the URI-encoded path (which includes the
	// bucket for path-style requests) followed by any signed sub-resources.
	resource := r.URL.EscapedPath()
	separator := "?"
	for _, name := range presignedV2ResourceSubresources {
		v := q.Get(name)
		if v == "" {
			continue
		}

		resource += separator + name + "=" + v
		separator = "&"
	}

	// SigV2 string-to-sign for a query-string authenticated request:
	// VERB \n Content-MD5 \n Content-Type \n Expires \n CanonicalizedResource.
	stringToSign := strings.Join([]string{
		r.Method,
		r.Header.Get("Content-MD5"),
		r.Header.Get("Content-Type"),
		expiresStr,
		resource,
	}, "\n")

	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte(stringToSign))
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(providedSignature)) {
		return "", &s3.Error{Code: s3.ErrorInvalidRequest, Message: "Signature mismatch."}
	}

	return role, nil
}

// hasAWSChunkedEncoding returns true if the request advertises an
// aws-chunked Content-Encoding. Several layers of clients (notably
// aws-sdk-go-v2 with checksum middleware disabled, or callers that
// pre-buffer the body) will set X-Amz-Content-Sha256 to a STREAMING-...
// sentinel without actually framing the body in aws-chunked. Use the
// content encoding as the authoritative signal for whether to invoke the
// chunked decoder.
func hasAWSChunkedEncoding(r *http.Request) bool {
	for _, v := range r.Header.Values("Content-Encoding") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "aws-chunked") {
				return true
			}
		}
	}

	return false
}

// wrapStreamingBody replaces r.Body with a reader that decodes the
// aws-chunked envelope. For the signed payload variants, per-chunk
// signatures are verified against the rolling previous-signature seeded
// from the request seed signature in parsed.signature.
//
// The decoded content length is taken from x-amz-decoded-content-length and
// reflected in r.ContentLength so downstream handlers see the true size of
// the payload they will read.
func wrapStreamingBody(r *http.Request, bodyHash string, parsed *parsedAuthorization, signingKey []byte) error {
	var sign *chunkSigningContext
	switch bodyHash {
	case streamingPayload:
		sign = &chunkSigningContext{
			algorithm:     "AWS4-HMAC-SHA256-PAYLOAD",
			signingKey:    signingKey,
			amzDate:       parsed.amzDate,
			scope:         parsed.scope,
			prevSignature: parsed.signature,
		}

	case streamingPayloadTrailer:
		sign = &chunkSigningContext{
			algorithm:     "AWS4-HMAC-SHA256-PAYLOAD-TRAILER",
			signingKey:    signingKey,
			amzDate:       parsed.amzDate,
			scope:         parsed.scope,
			prevSignature: parsed.signature,
		}

	case streamingUnsignedTrailer:
		sign = nil

	default:
		return fmt.Errorf("unsupported streaming body hash %q", bodyHash)
	}

	v := r.Header.Get("X-Amz-Decoded-Content-Length")
	if v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid X-Amz-Decoded-Content-Length %q", v)
		}

		r.ContentLength = n
	} else {
		// Without a decoded length we cannot pre-size the body. Mark
		// it as unknown so downstream code that streams the body
		// behaves correctly.
		r.ContentLength = -1
	}

	body := r.Body
	r.Body = &readerCloser{
		Reader: newChunkedReader(body, sign),
		Closer: body,
	}

	return nil
}

// readerCloser bundles a Reader and an independent Closer so we can wrap
// a request body's bytes through a decoder while still closing the
// original underlying body.
type readerCloser struct {
	io.Reader
	io.Closer
}

// parsedAuthorization holds the components of an AWS4-HMAC-SHA256 header.
type parsedAuthorization struct {
	accessKey     string
	scope         string
	scopeDate     string
	scopeRegion   string
	scopeService  string
	signedHeaders []string
	signature     string
	amzDate       string
}

// parseAuthorizationHeader parses an "AWS4-HMAC-SHA256 Credential=...,
// SignedHeaders=..., Signature=..." header.
func parseAuthorizationHeader(h string) (*parsedAuthorization, error) {
	const prefix = "AWS4-HMAC-SHA256"
	rest, ok := strings.CutPrefix(h, prefix)
	if !ok {
		return nil, fmt.Errorf("Authorization header is not AWS4-HMAC-SHA256")
	}

	rest = strings.TrimSpace(rest)

	out := &parsedAuthorization{}
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}

		switch k {
		case "Credential":
			// <accessKey>/<date>/<region>/<service>/aws4_request
			//
			// Access keys may contain "/" so do a reverse split.
			fields := strings.Split(v, "/")
			if len(fields) < 5 {
				return nil, fmt.Errorf("malformed Credential field")
			}

			out.accessKey = strings.Join(fields[:len(fields)-4], "/")
			out.scopeDate = fields[len(fields)-4]
			out.scopeRegion = fields[len(fields)-3]
			out.scopeService = fields[len(fields)-2]
			out.scope = strings.Join(fields[len(fields)-4:], "/")
		case "SignedHeaders":
			out.signedHeaders = strings.Split(v, ";")
			sort.Strings(out.signedHeaders)
		case "Signature":
			out.signature = v
		}
	}

	if out.accessKey == "" || out.signature == "" || len(out.signedHeaders) == 0 {
		return nil, fmt.Errorf("incomplete Authorization header")
	}

	return out, nil
}

// canonicalRequest builds the canonical request string defined by SigV4.
func canonicalRequest(r *http.Request, query url.Values, signedHeaders []string, bodyHash string) string {
	var sb strings.Builder

	sb.WriteString(r.Method)
	sb.WriteByte('\n')

	sb.WriteString(canonicalURI(r.URL.Path))
	sb.WriteByte('\n')

	sb.WriteString(canonicalQueryString(query))
	sb.WriteByte('\n')

	for _, name := range signedHeaders {
		sb.WriteString(name)
		sb.WriteByte(':')
		sb.WriteString(canonicalHeaderValue(r, name))
		sb.WriteByte('\n')
	}

	sb.WriteByte('\n')
	sb.WriteString(strings.Join(signedHeaders, ";"))
	sb.WriteByte('\n')
	sb.WriteString(bodyHash)

	return sb.String()
}

// canonicalURI returns the URI with each path segment URI-encoded once.
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}

	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = uriEncode(seg, false)
	}

	return strings.Join(segments, "/")
}

// canonicalQueryString returns query parameters sorted by name then value,
// each name and value URI-encoded.
func canonicalQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var sb strings.Builder
	first := true
	for _, k := range keys {
		vals := values[k]
		sort.Strings(vals)
		for _, v := range vals {
			if !first {
				sb.WriteByte('&')
			}

			first = false
			sb.WriteString(uriEncode(k, true))
			sb.WriteByte('=')
			sb.WriteString(uriEncode(v, true))
		}
	}

	return sb.String()
}

// canonicalHeaderValue returns the trimmed value of header name, with
// whitespace runs collapsed.
func canonicalHeaderValue(r *http.Request, name string) string {
	if name == "host" {
		return r.Host
	}

	v := r.Header.Get(name)
	v = strings.TrimSpace(v)
	for strings.Contains(v, "  ") {
		v = strings.ReplaceAll(v, "  ", " ")
	}

	return v
}

// uriEncode percent-encodes s per RFC 3986. Slashes are preserved when
// encodeSlash is false.
func uriEncode(s string, encodeSlash bool) string {
	var sb strings.Builder

	for _, r := range []byte(s) {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-', r == '~', r == '.':
			sb.WriteByte(r)
		case r == '/' && !encodeSlash:
			sb.WriteByte(r)
		default:
			fmt.Fprintf(&sb, "%%%02X", r)
		}
	}

	return sb.String()
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	return mac.Sum(nil)
}

func hmacSHA256Hex(key []byte, msg string) string {
	return hex.EncodeToString(hmacSHA256(key, msg))
}

func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

// SignRequest signs r in place using SigV4 with the given credential. It is
// exposed for tests and not used by the server.
func SignRequest(r *http.Request, accessKey, secretKey, region, service string, body []byte, now time.Time) error {
	amzDate := now.UTC().Format("20060102T150405Z")
	scopeDate := now.UTC().Format("20060102")

	r.Header.Set("X-Amz-Date", amzDate)
	if r.Header.Get("Host") == "" {
		r.Host = r.URL.Host
	}

	bodyHash := unsignedPayload
	if body != nil {
		bodyHash = sha256Hex(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
	}

	r.Header.Set("X-Amz-Content-Sha256", bodyHash)

	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	sort.Strings(signed)

	canonical := canonicalRequest(r, r.URL.Query(), signed, bodyHash)
	scope := strings.Join([]string{scopeDate, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")

	key := deriveSigningKey(secretKey, scopeDate, region, service)
	sig := hmacSHA256Hex(key, stringToSign)

	r.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		accessKey, scope, strings.Join(signed, ";"), sig,
	))

	return nil
}
