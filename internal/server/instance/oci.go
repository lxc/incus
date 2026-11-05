package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ociSpecs "github.com/opencontainers/runtime-spec/specs-go"
)

// OCISpec returns the parsed OCI config of an instance, nil when it's not an application container.
func OCISpec(instPath string) (*ociSpecs.Spec, error) {
	f, err := os.Open(filepath.Join(instPath, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if !fi.Mode().IsRegular() || fi.Size() > FileMaxSize {
		return nil, errors.New("Invalid OCI config file")
	}

	data, err := io.ReadAll(io.LimitReader(f, FileMaxSize))
	if err != nil {
		return nil, err
	}

	var spec ociSpecs.Spec
	err = json.Unmarshal(data, &spec)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing OCI config: %w", err)
	}

	if spec.Process == nil {
		return nil, errors.New("Failed parsing OCI config: Missing process section")
	}

	return &spec, nil
}

// OCIEnvironment returns the environment variables defined by an OCI config.
func OCIEnvironment(spec *ociSpecs.Spec) (map[string]string, error) {
	env := map[string]string{}
	if spec == nil {
		return env, nil
	}

	for _, entry := range spec.Process.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("Bad OCI environment variable: %s", entry)
		}

		if strings.ContainsAny(entry, "\r\n") {
			return nil, fmt.Errorf("Bad OCI environment variable %q: Line breaks aren't allowed", key)
		}

		env[key] = value
	}

	return env, nil
}
