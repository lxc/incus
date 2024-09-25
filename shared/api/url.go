package api

import (
	"net/url"
	"strings"
)

// URL represents an endpoint for the REST API.
type URL struct {
	url.URL
}

// NewURL creates a new URL.
func NewURL() *URL {
	return &URL{}
}

// Scheme sets the scheme of the URL.
func (u *URL) Scheme(scheme string) *URL {
	u.URL.Scheme = scheme

	return u
}

// Host sets the host of the URL.
func (u *URL) Host(host string) *URL {
	u.URL.Host = host

	return u
}

// Path sets the path of the URL from one or more path parts.
// It appends each of the pathParts (escaped using url.PathEscape) prefixed with "/" to the URL path.
func (u *URL) Path(pathParts ...string) *URL {
	var path, rawPath strings.Builder

	for _, pathPart := range pathParts {
		// Generate unencoded path.
		path.WriteString("/") // Build an absolute URL.
		path.WriteString(pathPart)

		// Generate encoded path hint (this will be used by u.URL.EncodedPath() to decide its methodology).
		rawPath.WriteString("/") // Build an absolute URL.
		rawPath.WriteString(url.PathEscape(pathPart))
	}

	u.URL.Path = path.String()
	u.URL.RawPath = rawPath.String()

	return u
}

// Project sets the "project" query parameter in the URL if the projectName is not empty or "default".
func (u *URL) Project(projectName string) *URL {
	if projectName != "default" && projectName != "" {
		queryArgs := u.Query()
		queryArgs.Add("project", projectName)
		u.RawQuery = queryArgs.Encode()
	}

	return u
}

// Target sets the "target" query parameter in the URL if the clusterMemberName is not empty or "default".
func (u *URL) Target(clusterMemberName string) *URL {
	if clusterMemberName != "" && clusterMemberName != "none" {
		queryArgs := u.Query()
		queryArgs.Add("target", clusterMemberName)
		u.RawQuery = queryArgs.Encode()
	}

	return u
}

// WithQuery adds a given query parameter with its value to the URL.
func (u *URL) WithQuery(key string, value string) *URL {
	queryArgs := u.Query()
	queryArgs.Add(key, value)
	u.RawQuery = queryArgs.Encode()

	return u
}
