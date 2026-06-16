package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"
)

// OCI registry media types (a subset, covering both the OCI and Docker schema 2 variants).
const (
	mediaTypeOCIIndex    = "application/vnd.oci.image.index.v1+json"
	mediaTypeOCIManifest = "application/vnd.oci.image.manifest.v1+json"
	mediaTypeDockerList  = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaTypeDockerV2    = "application/vnd.docker.distribution.manifest.v2+json"
)

// manifestAcceptHeaders is the set of manifest types we're willing to receive.
var manifestAcceptHeaders = []string{
	mediaTypeOCIIndex,
	mediaTypeOCIManifest,
	mediaTypeDockerList,
	mediaTypeDockerV2,
}

// descriptor is a content descriptor pointing at a manifest, config or layer.
type descriptor struct {
	MediaType string    `json:"mediaType"`
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	Platform  *platform `json:"platform,omitempty"`
}

// platform describes the platform a manifest targets.
type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

// manifestList is an image index (multi-arch manifest list).
type manifestList struct {
	Manifests []descriptor `json:"manifests"`
}

// manifest is a single-platform image manifest.
type manifest struct {
	Config descriptor   `json:"config"`
	Layers []descriptor `json:"layers"`
}

// imageConfig is the (subset of the) image config blob we care about.
type imageConfig struct {
	Architecture string     `json:"architecture"`
	Created      *time.Time `json:"created"`
}

// Layer describes a single image layer.
type Layer struct {
	Digest string
	Size   int64
}

// ImageInfo holds the subset of OCI image metadata that Incus needs.
type ImageInfo struct {
	// Name is the fully-qualified image name (host/repository).
	Name string

	// Architecture is the image architecture, using OCI naming (e.g. "amd64").
	Architecture string

	// Created is the image creation timestamp.
	Created time.Time

	// Layers are the image layers, in manifest order.
	Layers []Layer
}

// Registry is a minimal client for the OCI distribution API. It talks to a
// registry directly over HTTP, avoiding any dependency on the "skopeo" binary.
type Registry struct {
	client    *http.Client
	host      string
	userAgent string

	// Bearer tokens cached per authentication scope.
	tokensMu sync.Mutex
	tokens   map[string]*scopeToken
}

// scopeToken holds the cached bearer token for a single authentication scope.
// Its mutex serialises fetches so the token is only retrieved once per scope,
// even when multiple requests race.
type scopeToken struct {
	mu    sync.Mutex
	value string
}

// NewRegistry returns a Registry for the given remote URL. The provided HTTP
// client is used for all requests (so TLS and proxy settings are honoured); if
// nil, http.DefaultClient is used.
func NewRegistry(host string, client *http.Client, userAgent string) *Registry {
	if client == nil {
		client = http.DefaultClient
	}

	return &Registry{
		client:    client,
		host:      host,
		userAgent: userAgent,
		tokens:    map[string]*scopeToken{},
	}
}

// tokenFor returns the token holder for a scope, creating it on first use.
func (r *Registry) tokenFor(scope string) *scopeToken {
	r.tokensMu.Lock()
	defer r.tokensMu.Unlock()

	t := r.tokens[scope]
	if t == nil {
		t = &scopeToken{}
		r.tokens[scope] = t
	}

	return t
}

// cachedToken returns the currently cached bearer token for a scope, if any. It
// blocks while another request is fetching the token for the same scope, so the
// fresh token is reused rather than triggering a redundant fetch.
func (r *Registry) cachedToken(scope string) string {
	t := r.tokenFor(scope)

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.value
}

// bearerToken returns a bearer token for the given scope, fetching it from the
// challenge realm at most once per scope. stale is the token that just failed
// authentication (if any); when it still matches the cached value a refresh is
// forced, which handles token expiry.
func (r *Registry) bearerToken(scope string, challenge string, stale string, user *url.Userinfo) (string, error) {
	t := r.tokenFor(scope)

	t.mu.Lock()
	defer t.mu.Unlock()

	// A concurrent request may already have fetched a fresh token.
	if t.value != "" && t.value != stale {
		return t.value, nil
	}

	value, err := r.fetchToken(challenge, user)
	if err != nil {
		return "", err
	}

	t.value = value

	return value, nil
}

// Inspect fetches image metadata for the given image name. It is the native
// equivalent of "skopeo inspect" for the fields Incus needs.
func (r *Registry) Inspect(name string) (*ImageInfo, error) {
	// Parse the configured remote URL for the registry host and any credentials.
	registry, err := url.Parse(r.host)
	if err != nil {
		return nil, err
	}

	scheme := registry.Scheme
	if scheme == "" {
		scheme = "https"
	}

	// Split the image reference into a repository and a reference (tag or digest).
	repo, ref := splitRef(name)

	// Handle the Docker Hub naming quirks (API host and "library/" default namespace).
	apiHost := registry.Host
	if apiHost == "docker.io" || apiHost == "index.docker.io" {
		apiHost = "registry-1.docker.io"
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
	}

	// Fetch the top-level manifest, which may be a multi-arch index.
	body, mediaType, err := r.fetchManifest(scheme, apiHost, repo, ref, registry.User)
	if err != nil {
		return nil, err
	}

	// If we got an index, pick the manifest matching the local architecture.
	if isManifestList(mediaType) {
		var index manifestList
		err = json.Unmarshal(body, &index)
		if err != nil {
			return nil, err
		}

		desc := selectPlatform(index.Manifests)
		if desc == nil {
			return nil, fmt.Errorf("No matching image found for architecture %q", runtime.GOARCH)
		}

		body, _, err = r.fetchManifest(scheme, apiHost, repo, desc.Digest, registry.User)
		if err != nil {
			return nil, err
		}
	}

	// Parse the image manifest.
	var imageManifest manifest
	err = json.Unmarshal(body, &imageManifest)
	if err != nil {
		return nil, err
	}

	if imageManifest.Config.Digest == "" {
		return nil, errors.New("Registry returned a manifest without an image config")
	}

	// Fetch the config blob for the architecture and creation date.
	configBody, err := r.fetchBlob(scheme, apiHost, repo, imageManifest.Config.Digest, registry.User)
	if err != nil {
		return nil, err
	}

	var config imageConfig
	err = json.Unmarshal(configBody, &config)
	if err != nil {
		return nil, err
	}

	info := &ImageInfo{
		Name:         fmt.Sprintf("%s/%s", registry.Host, repo),
		Architecture: config.Architecture,
	}

	if config.Created != nil {
		info.Created = *config.Created
	}

	for _, layer := range imageManifest.Layers {
		info.Layers = append(info.Layers, Layer{Digest: layer.Digest, Size: layer.Size})
	}

	return info, nil
}

// doHTTP performs an HTTP request, setting the configured user agent.
func (r *Registry) doHTTP(req *http.Request) (*http.Response, error) {
	if r.userAgent != "" {
		req.Header.Set("User-Agent", r.userAgent)
	}

	return r.client.Do(req)
}

// fetchManifest retrieves a manifest (or index) and returns its body and media type.
func (r *Registry) fetchManifest(scheme string, host string, repo string, ref string, user *url.Userinfo) ([]byte, string, error) {
	reqURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, host, repo, ref)

	resp, err := r.registryRequest("GET", reqURL, pullScope(repo), manifestAcceptHeaders, user)
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	return body, resp.Header.Get("Content-Type"), nil
}

// fetchBlob retrieves a blob (such as the image config) by digest.
func (r *Registry) fetchBlob(scheme string, host string, repo string, digest string, user *url.Userinfo) ([]byte, error) {
	reqURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", scheme, host, repo, digest)

	resp, err := r.registryRequest("GET", reqURL, pullScope(repo), nil, user)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(resp.Body)
}

// registryRequest performs a registry API request, transparently handling the
// authentication challenge used by most OCI registries. A bearer token obtained
// for the given scope is cached on the Registry and reused across requests.
func (r *Registry) registryRequest(method string, reqURL string, scope string, accept []string, user *url.Userinfo) (*http.Response, error) {
	newRequest := func() (*http.Request, error) {
		req, err := http.NewRequest(method, reqURL, nil)
		if err != nil {
			return nil, err
		}

		for _, a := range accept {
			req.Header.Add("Accept", a)
		}

		return req, nil
	}

	req, err := newRequest()
	if err != nil {
		return nil, err
	}

	// Reuse a cached token for this scope to avoid an authentication round-trip.
	bearer := r.cachedToken(scope)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := r.doHTTP(req)
	if err != nil {
		return nil, err
	}

	// Handle the authentication challenge (also covers an expired cached token).
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("Www-Authenticate")
		_ = resp.Body.Close()

		req, err = newRequest()
		if err != nil {
			return nil, err
		}

		err = r.applyAuth(req, challenge, scope, bearer, user)
		if err != nil {
			return nil, err
		}

		resp, err = r.doHTTP(req)
		if err != nil {
			return nil, err
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		return nil, fmt.Errorf("Registry returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return resp, nil
}

// applyAuth satisfies a "Www-Authenticate" challenge on req, using the provided
// credentials. It supports both the bearer token flow used by most registries
// and direct HTTP basic authentication.
func (r *Registry) applyAuth(req *http.Request, challenge string, scope string, stale string, user *url.Userinfo) error {
	switch {
	case strings.HasPrefix(challenge, "Bearer "):
		token, err := r.bearerToken(scope, strings.TrimPrefix(challenge, "Bearer "), stale, user)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", "Bearer "+token)

		return nil

	case strings.HasPrefix(challenge, "Basic"):
		if user == nil {
			return errors.New("Registry requires authentication but no credentials were provided")
		}

		password, _ := user.Password()
		req.SetBasicAuth(user.Username(), password)

		return nil

	default:
		return fmt.Errorf("Unsupported registry authentication challenge: %q", challenge)
	}
}

// fetchToken resolves a bearer token from the realm advertised in a bearer
// "Www-Authenticate" challenge, optionally authenticating with the provided
// credentials. params is the challenge with its "Bearer " prefix removed.
func (r *Registry) fetchToken(params string, user *url.Userinfo) (string, error) {
	authParams := parseAuthParams(params)

	realm := authParams["realm"]
	if realm == "" {
		return "", errors.New("Registry authentication challenge is missing a realm")
	}

	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", err
	}

	query := tokenURL.Query()
	if authParams["service"] != "" {
		query.Set("service", authParams["service"])
	}

	if authParams["scope"] != "" {
		query.Set("scope", authParams["scope"])
	}

	tokenURL.RawQuery = query.Encode()

	req, err := http.NewRequest("GET", tokenURL.String(), nil)
	if err != nil {
		return "", err
	}

	// Use the configured credentials to obtain a scoped token (for private images).
	if user != nil {
		password, _ := user.Password()
		req.SetBasicAuth(user.Username(), password)
	}

	resp, err := r.doHTTP(req)
	if err != nil {
		return "", err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Failed getting registry token (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Registries return the token as either "token" or "access_token".
	var data struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return "", err
	}

	if data.Token != "" {
		return data.Token, nil
	}

	if data.AccessToken != "" {
		return data.AccessToken, nil
	}

	return "", errors.New("Registry returned an empty authentication token")
}

// pullScope returns the registry authentication scope for pulling a repository.
func pullScope(repo string) string {
	return fmt.Sprintf("repository:%s:pull", repo)
}

// splitRef splits an image name into a repository and a reference (tag or digest).
func splitRef(name string) (string, string) {
	// A digest pin takes precedence over any tag.
	repo, digest, hasDigest := strings.Cut(name, "@")
	if hasDigest {
		return repo, digest
	}

	// A tag is the part after the last colon, as long as it isn't part of a host:port.
	slash := strings.LastIndex(name, "/")
	colon := strings.LastIndex(name, ":")
	if colon > slash {
		return name[:colon], name[colon+1:]
	}

	return name, "latest"
}

// selectPlatform returns the manifest descriptor matching the local Linux architecture.
func selectPlatform(manifests []descriptor) *descriptor {
	for i := range manifests {
		p := manifests[i].Platform
		if p == nil || p.OS != "linux" {
			continue
		}

		// OCI architecture names line up with Go's GOARCH values.
		if p.Architecture == runtime.GOARCH {
			return &manifests[i]
		}
	}

	return nil
}

// isManifestList reports whether the given media type is a multi-arch index.
func isManifestList(mediaType string) bool {
	mediaType, _, _ = strings.Cut(mediaType, ";")
	mediaType = strings.TrimSpace(mediaType)

	return mediaType == mediaTypeOCIIndex || mediaType == mediaTypeDockerList
}

// parseAuthParams parses the comma-separated key="value" parameters of a
// "Www-Authenticate" challenge.
func parseAuthParams(s string) map[string]string {
	params := map[string]string{}

	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")

		eq := strings.Index(s, "=")
		if eq < 0 {
			break
		}

		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]

		var value string
		if strings.HasPrefix(s, "\"") {
			s = s[1:]

			end := strings.Index(s, "\"")
			if end < 0 {
				value = s
				s = ""
			} else {
				value = s[:end]
				s = s[end+1:]
			}
		} else {
			end := strings.Index(s, ",")
			if end < 0 {
				value = s
				s = ""
			} else {
				value = s[:end]
				s = s[end+1:]
			}
		}

		params[key] = value
	}

	return params
}
