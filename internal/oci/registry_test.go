package oci

import (
	"testing"
)

func TestSplitRef(t *testing.T) {
	cases := []struct {
		name     string
		wantRepo string
		wantRef  string
	}{
		{"alpine", "alpine", "latest"},
		{"alpine:3.19", "alpine", "3.19"},
		{"library/alpine:3.19", "library/alpine", "3.19"},
		{"alpine@sha256:abc", "alpine", "sha256:abc"},
		{"library/alpine@sha256:abc", "library/alpine", "sha256:abc"},
		{"ghcr.io/foo/bar:tag", "ghcr.io/foo/bar", "tag"},
	}

	for _, c := range cases {
		repo, ref := splitRef(c.name)
		if repo != c.wantRepo || ref != c.wantRef {
			t.Errorf("splitRef(%q) = (%q, %q), want (%q, %q)", c.name, repo, ref, c.wantRepo, c.wantRef)
		}
	}
}

func TestParseAuthParams(t *testing.T) {
	in := `realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:library/alpine:pull"`

	params := parseAuthParams(in)
	if params["realm"] != "https://auth.docker.io/token" {
		t.Errorf("realm = %q", params["realm"])
	}

	if params["service"] != "registry.docker.io" {
		t.Errorf("service = %q", params["service"])
	}

	if params["scope"] != "repository:library/alpine:pull" {
		t.Errorf("scope = %q", params["scope"])
	}
}

func TestIsManifestList(t *testing.T) {
	if !isManifestList(mediaTypeOCIIndex) {
		t.Error("OCI index should be a manifest list")
	}

	if !isManifestList(mediaTypeDockerList + "; charset=utf-8") {
		t.Error("Docker list with charset should be a manifest list")
	}

	if isManifestList(mediaTypeOCIManifest) {
		t.Error("OCI manifest should not be a manifest list")
	}
}
