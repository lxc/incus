package incus

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ioprogress"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/units"
)

type ociInfo struct {
	Alias        string
	Name         string    `json:"Name"`
	Digest       string    `json:"Digest"`
	Created      time.Time `json:"Created"`
	Architecture string    `json:"Architecture"`
	Layers       []string  `json:"Layers"`
	LayersData   []struct {
		Size int64 `json:"Size"`
	} `json:"LayersData"`
}

// Get the proxy host value.
func (r *ProtocolOCI) getProxyHost() (*url.URL, error) {
	req, err := http.NewRequest("GET", r.httpHost, nil)
	if err != nil {
		return nil, err
	}

	proxy, err := r.http.Transport.(*http.Transport).Proxy(req)
	if err != nil {
		return nil, err
	}

	return proxy, nil
}

// Image handling functions

// GetImages returns a list of available images as Image structs.
func (r *ProtocolOCI) GetImages() ([]api.Image, error) {
	return nil, errors.New("Can't list images from OCI registry")
}

// GetImagesAllProjects returns a list of available images as Image structs.
func (r *ProtocolOCI) GetImagesAllProjects() ([]api.Image, error) {
	return nil, errors.New("Can't list images from OCI registry")
}

// GetImagesAllProjectsWithFilter returns a filtered list of available images as Image structs.
func (r *ProtocolOCI) GetImagesAllProjectsWithFilter(filters []string) ([]api.Image, error) {
	return nil, errors.New("Can't list images from OCI registry")
}

// GetImageFingerprints returns a list of available image fingerprints.
func (r *ProtocolOCI) GetImageFingerprints() ([]string, error) {
	return nil, errors.New("Can't list images from OCI registry")
}

// GetImagesWithFilter returns a filtered list of available images as Image structs.
func (r *ProtocolOCI) GetImagesWithFilter(_ []string) ([]api.Image, error) {
	return nil, errors.New("Can't list images from OCI registry")
}

// GetImage returns an Image struct for the provided fingerprint.
func (r *ProtocolOCI) GetImage(fingerprint string) (*api.Image, string, error) {
	info, ok := r.cache[fingerprint]
	if !ok {
		_, err := exec.LookPath("skopeo")
		if err != nil {
			return nil, "", errors.New("OCI container handling requires \"skopeo\" be present on the system")
		}

		return nil, "", errors.New("Image not found")
	}

	img := api.Image{
		ImagePut: api.ImagePut{
			Public: true,
			Properties: map[string]string{
				"architecture": info.Architecture,
				"type":         "oci",
				"description":  fmt.Sprintf("%s (OCI)", info.Name),
				"id":           info.Alias,
			},
		},
		Aliases: []api.ImageAlias{{
			Name: info.Alias,
		}},
		Architecture: info.Architecture,
		Fingerprint:  fingerprint,
		Type:         string(api.InstanceTypeContainer),
		CreatedAt:    info.Created,
		UploadedAt:   info.Created,
	}

	var size int64
	for _, layer := range info.LayersData {
		size += layer.Size
	}

	img.Size = size

	return &img, "", nil
}

// GetImageFile downloads an image from the server, returning an ImageFileResponse struct.
func (r *ProtocolOCI) GetImageFile(fingerprint string, req ImageFileRequest) (*ImageFileResponse, error) {
	ctx := context.Background()

	// Get the cached entry.
	info, ok := r.cache[fingerprint]
	if !ok {
		_, err := exec.LookPath("skopeo")
		if err != nil {
			return nil, errors.New("OCI container handling requires \"skopeo\" be present on the system")
		}

		return nil, errors.New("Image not found")
	}

	// Quick checks.
	if req.MetaFile == nil && req.RootfsFile == nil {
		return nil, errors.New("No file requested")
	}

	if os.Geteuid() != 0 {
		return nil, errors.New("OCI image export currently requires root access")
	}

	// Get some temporary storage.
	ociPath, err := os.MkdirTemp(r.tempPath, "incus-oci-")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.RemoveAll(ociPath) }()

	err = os.Mkdir(filepath.Join(ociPath, "oci"), 0o700)
	if err != nil {
		return nil, err
	}

	err = os.Mkdir(filepath.Join(ociPath, "image"), 0o700)
	if err != nil {
		return nil, err
	}

	// Copy the image.
	if req.ProgressHandler != nil {
		req.ProgressHandler(ioprogress.ProgressData{Text: "Retrieving OCI image from registry"})
	}

	imageTag := "latest"

	stdout, err := r.runSkopeo(
		"copy", info.Alias,
		"--remove-signatures",
		fmt.Sprintf("oci:%s:%s", filepath.Join(ociPath, "oci"), imageTag))
	if err != nil {
		logger.Debug("Error copying remote image to local", logger.Ctx{"image": info.Alias, "stdout": stdout, "stderr": err})
		return nil, err
	}

	// Convert to something usable.
	if req.ProgressHandler != nil {
		req.ProgressHandler(ioprogress.ProgressData{Text: "Unpacking the OCI image"})
	}

	err = unpackOCIImage(filepath.Join(ociPath, "oci"), imageTag, filepath.Join(ociPath, "image"))
	if err != nil {
		logger.Debug("Error unpacking OCI image", logger.Ctx{"image": filepath.Join(ociPath, "oci"), "err": err})
		return nil, err
	}

	// Generate a metadata.yaml.
	if req.ProgressHandler != nil {
		req.ProgressHandler(ioprogress.ProgressData{Text: "Generating image metadata"})
	}

	metadata := api.ImageMetadata{
		Architecture: info.Architecture,
		CreationDate: info.Created.Unix(),
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(filepath.Join(ociPath, "image", "metadata.yaml"), data, 0o644)
	if err != nil {
		return nil, err
	}

	// Prepare response.
	resp := &ImageFileResponse{
		MetaName:   "metadata.tar.gz",
		RootfsName: "rootfs.tar.gz",
	}

	// Prepare to push the tarballs.
	var pipeRead io.ReadCloser
	var pipeWrite io.WriteCloser

	// Push the metadata tarball.
	pipeRead, pipeWrite = io.Pipe()
	defer pipeRead.Close()
	defer pipeWrite.Close()

	if req.ProgressHandler != nil {
		pipeRead = &ioprogress.ProgressReader{
			ReadCloser: pipeRead,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(received int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("Generating metadata tarball: %s (%s/s)", units.GetByteSizeString(received, 2), units.GetByteSizeString(speed, 2))})
				},
			},
		}
	}

	compressWrite := gzip.NewWriter(pipeWrite)
	metadataProcess := subprocess.NewProcessWithFds("tar", []string{"-cf", "-", "-C", filepath.Join(ociPath, "image"), "config.json", "metadata.yaml"}, nil, compressWrite, os.Stderr)
	err = metadataProcess.Start(ctx)
	if err != nil {
		return nil, err
	}

	go func() {
		_, _ = metadataProcess.Wait(ctx)
		compressWrite.Close()
		pipeWrite.Close()
	}()

	size, err := io.Copy(req.MetaFile, pipeRead)
	if err != nil {
		return nil, err
	}

	resp.MetaSize = size

	// Push the rootfs tarball.
	pipeRead, pipeWrite = io.Pipe()
	defer pipeRead.Close()
	defer pipeWrite.Close()

	if req.ProgressHandler != nil {
		pipeRead = &ioprogress.ProgressReader{
			ReadCloser: pipeRead,
			Tracker: &ioprogress.ProgressTracker{
				Handler: func(received int64, speed int64) {
					req.ProgressHandler(ioprogress.ProgressData{Text: fmt.Sprintf("Generating rootfs tarball: %s (%s/s)", units.GetByteSizeString(received, 2), units.GetByteSizeString(speed, 2))})
				},
			},
		}
	}

	compressWrite = gzip.NewWriter(pipeWrite)
	rootfsProcess := subprocess.NewProcessWithFds("tar", []string{"-cf", "-", "-C", filepath.Join(ociPath, "image", "rootfs"), "."}, nil, compressWrite, nil)
	err = rootfsProcess.Start(ctx)
	if err != nil {
		return nil, err
	}

	go func() {
		_, _ = rootfsProcess.Wait(ctx)
		compressWrite.Close()
		pipeWrite.Close()
	}()

	size, err = io.Copy(req.RootfsFile, pipeRead)
	if err != nil {
		return nil, err
	}

	resp.RootfsSize = size

	return resp, nil
}

// GetImageSecret isn't relevant for the simplestreams protocol.
func (r *ProtocolOCI) GetImageSecret(_ string) (string, error) {
	return "", errors.New("Private images aren't supported with OCI registry")
}

// GetPrivateImage isn't relevant for the simplestreams protocol.
func (r *ProtocolOCI) GetPrivateImage(_ string, _ string) (*api.Image, string, error) {
	return nil, "", errors.New("Private images aren't supported with OCI registry")
}

// GetPrivateImageFile isn't relevant for the simplestreams protocol.
func (r *ProtocolOCI) GetPrivateImageFile(_ string, _ string, _ ImageFileRequest) (*ImageFileResponse, error) {
	return nil, errors.New("Private images aren't supported with OCI registry")
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs.
func (r *ProtocolOCI) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	return nil, errors.New("Can't list image aliases from OCI registry")
}

// GetImageAliasNames returns the list of available alias names.
func (r *ProtocolOCI) GetImageAliasNames() ([]string, error) {
	return nil, errors.New("Can't list image aliases from OCI registry")
}

func (r *ProtocolOCI) runSkopeo(action string, image string, args ...string) (string, error) {
	// Parse and mangle the server URL.
	uri, err := url.Parse(r.httpHost)
	if err != nil {
		return "", err
	}

	// Get proxy details.
	proxy, err := r.getProxyHost()
	if err != nil {
		return "", err
	}

	var env []string
	if proxy != nil {
		env = []string{
			fmt.Sprintf("HTTPS_PROXY=%s", proxy),
			fmt.Sprintf("HTTP_PROXY=%s", proxy),
		}
	}

	// Handle authentication.
	if uri.User != nil {
		creds, err := json.Marshal(map[string]any{
			"auths": map[string]any{
				uri.Scheme + "://" + uri.Host: map[string]string{
					"auth": base64.StdEncoding.EncodeToString([]byte(uri.User.String())),
				},
			},
		})
		if err != nil {
			return "", err
		}

		authFile, err := os.CreateTemp(r.tempPath, "incus_client_auth_")
		if err != nil {
			return "", err
		}

		defer authFile.Close()
		defer os.Remove(authFile.Name())

		err = authFile.Chmod(0o600)
		if err != nil {
			return "", err
		}

		_, err = fmt.Fprintf(authFile, "%s", creds)
		if err != nil {
			return "", err
		}

		uri.User = nil

		args = append(args, fmt.Sprintf("--authfile=%s", authFile.Name()))
	}

	// Prepare the arguments.
	uri.Scheme = "docker"
	args = append([]string{"--insecure-policy", action, fmt.Sprintf("%s/%s", uri.String(), image)}, args...)

	// Get the image information from skopeo.
	stdout, _, err := subprocess.RunCommandSplit(
		context.TODO(),
		env,
		nil,
		"skopeo",
		args...)
	if err != nil {
		return "", err
	}

	return stdout, nil
}

// GetImageAlias returns an existing alias as an ImageAliasesEntry struct.
func (r *ProtocolOCI) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	// If image name is "IMAGE:TAG@HASH", drop ":TAG" so that skopeo uses the pinned hash instead.
	imageWithoutHash, hash, hasHash := strings.Cut(name, "@")
	if hasHash {
		imageWithoutTag, _, _ := strings.Cut(imageWithoutHash, ":")
		name = fmt.Sprintf("%s@%s", imageWithoutTag, hash)
	}

	// Get the image information from skopeo.
	stdout, err := r.runSkopeo("inspect", name)
	if err != nil {
		logger.Debug("Error getting image alias", logger.Ctx{"name": name, "stdout": stdout, "stderr": err})
		return nil, "", err
	}

	// Parse the image info.
	var info ociInfo
	err = json.Unmarshal([]byte(stdout), &info)
	if err != nil {
		return nil, "", err
	}

	info.Alias = name
	info.Digest = r.computeFingerprint(info.Layers)

	archID, err := osarch.ArchitectureID(info.Architecture)
	if err != nil {
		return nil, "", err
	}

	archName, err := osarch.ArchitectureName(archID)
	if err != nil {
		return nil, "", err
	}

	info.Architecture = archName

	// Store it in the cache.
	r.cache[info.Digest] = info

	// Prepare the alias entry.
	alias := api.ImageAliasesEntry{
		ImageAliasesEntryPut: api.ImageAliasesEntryPut{
			Target: info.Digest,
		},
		Name: name,
		Type: string(api.InstanceTypeContainer),
	}

	return &alias, "", nil
}

// GetImageAliasType returns an existing alias as an ImageAliasesEntry struct.
func (r *ProtocolOCI) GetImageAliasType(imageType string, name string) (*api.ImageAliasesEntry, string, error) {
	if api.InstanceType(imageType) == api.InstanceTypeVM {
		return nil, "", errors.New("OCI images are only supported for containers")
	}

	return r.GetImageAlias(name)
}

// GetImageAliasArchitectures returns a map of architectures / targets.
func (r *ProtocolOCI) GetImageAliasArchitectures(imageType string, name string) (map[string]*api.ImageAliasesEntry, error) {
	if api.InstanceType(imageType) == api.InstanceTypeVM {
		return nil, errors.New("OCI images are only supported for containers")
	}

	alias, _, err := r.GetImageAlias(name)
	if err != nil {
		return nil, err
	}

	localArch, err := osarch.ArchitectureGetLocal()
	if err != nil {
		return nil, err
	}

	return map[string]*api.ImageAliasesEntry{localArch: alias}, nil
}

// ExportImage exports (copies) an image to a remote server.
func (r *ProtocolOCI) ExportImage(_ string, _ api.ImageExportPost) (Operation, error) {
	return nil, errors.New("Exporting images is not supported with OCI registry")
}

func (r *ProtocolOCI) computeFingerprint(layers []string) string {
	h := sha256.New()

	for _, layer := range layers {
		h.Write([]byte(layer))
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}
