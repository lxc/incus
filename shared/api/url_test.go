package api

import (
	"testing"
)

func TestScheme(t *testing.T) {
	tests := []struct {
		name     string
		scheme   string
		expected string
	}{
		{"http scheme", "http", "http"},
		{"https scheme", "https", "https"},
		{"empty scheme", "", ""},
		{"custom scheme", "ftp", "ftp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL()
			result := u.Scheme(tt.scheme)
			if u.URL.Scheme != tt.expected {
				t.Errorf("Expected scheme %q, got %q", tt.expected, u.URL.Scheme)
			}
			// Test chaining - should return the same instance
			if result != u {
				t.Error("Scheme() should return the same instance for chaining")
			}
		})
	}
}

func TestHost(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		expected string
	}{
		{"simple host", "example.com", "example.com"},
		{"host with port", "example.com:8080", "example.com:8080"},
		{"empty host", "", ""},
		{"IP address", "192.168.1.1", "192.168.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL().Host(tt.host)
			if u.URL.Host != tt.expected {
				t.Errorf("Expected host %q, got %q", tt.expected, u.URL.Host)
			}
		})
	}
}

func TestPath(t *testing.T) {
	tests := []struct {
		name         string
		pathParts    []string
		expectedPath string
		expectedRaw  string
	}{
		{
			name:         "empty path parts",
			pathParts:    []string{},
			expectedPath: "",
			expectedRaw:  "",
		},
		{
			name:         "single empty path part",
			pathParts:    []string{""},
			expectedPath: "/",
			expectedRaw:  "/",
		},
		{
			name:         "single path part",
			pathParts:    []string{"networks"},
			expectedPath: "/networks",
			expectedRaw:  "/networks",
		},
		{
			name:         "multiple path parts",
			pathParts:    []string{"1.0", "networks", "my-network"},
			expectedPath: "/1.0/networks/my-network",
			expectedRaw:  "/1.0/networks/my-network",
		},
		{
			name:         "path with slash",
			pathParts:    []string{"networks", "name-with-/-in-it"},
			expectedPath: "/networks/name-with-/-in-it",
			expectedRaw:  "/networks/name-with-%2F-in-it",
		},
		{
			name:         "path with percent",
			pathParts:    []string{"networks", "name-with-%-in-it"},
			expectedPath: "/networks/name-with-%-in-it",
			expectedRaw:  "/networks/name-with-%25-in-it",
		},
		{
			name:         "path with space",
			pathParts:    []string{"networks", "my network"},
			expectedPath: "/networks/my network",
			expectedRaw:  "/networks/my%20network",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL().Path(tt.pathParts...)
			if u.URL.Path != tt.expectedPath {
				t.Errorf("Expected path %q, got %q", tt.expectedPath, u.URL.Path)
			}

			if u.RawPath != tt.expectedRaw {
				t.Errorf("Expected raw path %q, got %q", tt.expectedRaw, u.RawPath)
			}
		})
	}
}

func TestProject(t *testing.T) {
	tests := []struct {
		name            string
		projectName     string
		shouldHaveQuery bool
		expectedValue   string
	}{
		{
			name:            "empty project name",
			projectName:     "",
			shouldHaveQuery: false,
		},
		{
			name:            "default project",
			projectName:     "default",
			shouldHaveQuery: false,
		},
		{
			name:            "custom project",
			projectName:     "my-project",
			shouldHaveQuery: true,
			expectedValue:   "my-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL().Project(tt.projectName)
			query := u.Query()

			if tt.shouldHaveQuery {
				if !query.Has("project") {
					t.Error("Expected query to have 'project' parameter")
				}

				if query.Get("project") != tt.expectedValue {
					t.Errorf("Expected project value %q, got %q", tt.expectedValue, query.Get("project"))
				}
			} else {
				if query.Has("project") {
					t.Errorf("Expected query to not have 'project' parameter, but got %q", query.Get("project"))
				}
			}
		})
	}
}

func TestTarget(t *testing.T) {
	tests := []struct {
		name            string
		clusterMember   string
		shouldHaveQuery bool
		expectedValue   string
	}{
		{
			name:            "empty target",
			clusterMember:   "",
			shouldHaveQuery: false,
		},
		{
			name:            "none target",
			clusterMember:   "none",
			shouldHaveQuery: false,
		},
		{
			name:            "custom target",
			clusterMember:   "member1",
			shouldHaveQuery: true,
			expectedValue:   "member1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL().Target(tt.clusterMember)
			query := u.Query()

			if tt.shouldHaveQuery {
				if !query.Has("target") {
					t.Error("Expected query to have 'target' parameter")
				}

				if query.Get("target") != tt.expectedValue {
					t.Errorf("Expected target value %q, got %q", tt.expectedValue, query.Get("target"))
				}
			} else {
				if query.Has("target") {
					t.Errorf("Expected query to not have 'target' parameter, but got %q", query.Get("target"))
				}
			}
		})
	}
}

func TestWithQuery(t *testing.T) {
	tests := []struct {
		name          string
		key           string
		value         string
		expectedKey   string
		expectedValue string
	}{
		{
			name:          "simple query",
			key:           "foo",
			value:         "bar",
			expectedKey:   "foo",
			expectedValue: "bar",
		},
		{
			name:          "empty value",
			key:           "empty",
			value:         "",
			expectedKey:   "empty",
			expectedValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewURL().WithQuery(tt.key, tt.value)
			query := u.Query()

			if !query.Has(tt.expectedKey) {
				t.Errorf("Expected query to have key %q", tt.expectedKey)
			}

			if query.Get(tt.expectedKey) != tt.expectedValue {
				t.Errorf("Expected value %q for key %q, got %q", tt.expectedValue, tt.expectedKey, query.Get(tt.expectedKey))
			}
		})
	}

	// Test multiple query parameters
	t.Run("multiple query parameters", func(t *testing.T) {
		u := NewURL().
			WithQuery("foo", "bar").
			WithQuery("recursion", "1")

		query := u.Query()
		if query.Get("foo") != "bar" {
			t.Errorf("Expected foo=bar, got %q", query.Get("filter"))
		}

		if query.Get("recursion") != "1" {
			t.Errorf("Expected recursion=1, got %q", query.Get("recursion"))
		}
	})
}

func TestURLString(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() *URL
		expected string
	}{
		{
			name: "full URL with all components",
			setup: func() *URL {
				return NewURL().
					Scheme("https").
					Host("example.com").
					Path("1.0", "networks", "test-network").
					Project("my-project").
					Target("member1")
			},
			expected: "https://example.com/1.0/networks/test-network?project=my-project&target=member1",
		},
		{
			name: "URL with encoded path",
			setup: func() *URL {
				return NewURL().
					Scheme("https").
					Host("example.com").
					Path("1.0", "networks", "name-with-/-in-it")
			},
			expected: "https://example.com/1.0/networks/name-with-%2F-in-it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := tt.setup()
			actual := u.String()
			if actual != tt.expected {
				t.Errorf("Expected URL %q, got %q", tt.expected, actual)
			}
		})
	}
}
