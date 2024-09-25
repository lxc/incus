package request

import (
	"net/http"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
)

// ProjectParam returns the project query parameter from the given request or "default" if parameter is not set.
func ProjectParam(request *http.Request) string {
	projectParam := QueryParam(request, "project")
	if projectParam == "" {
		projectParam = api.ProjectDefaultName
	}

	return projectParam
}

// QueryParam extracts the given query parameter directly from the URL, never from an
// encoded body.
func QueryParam(request *http.Request, key string) string {
	var values url.Values
	var err error

	if request.URL != nil {
		values, err = url.ParseQuery(request.URL.RawQuery)
		if err != nil {
			logger.Warnf("Failed to parse query string %q: %v", request.URL.RawQuery, err)
			return ""
		}
	}

	if values == nil {
		values = make(url.Values)
	}

	return values.Get(key)
}
