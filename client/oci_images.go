package incus

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/ioprogress"
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
	LayersData   []struct {
		Size int64 `json:"Size"`
	} `json:"LayersData"`
}

// Image handling functions

// GetImages returns a list of available images as Image structs.
func (r *ProtocolOCI) GetImages() ([]api.Image, error) {
	return nil, fmt.Errorf("Can't list images from OCI registry")
}

// GetImagesAllProjects returns a list of available images as Image structs.
func (r *ProtocolOCI) GetImagesAllProjects() ([]api.Image, error) {
	return nil, fmt.Errorf("Can't list images from OCI registry")
}

// GetImageFingerprints returns a list of available image fingerprints.
func (r *ProtocolOCI) GetImageFingerprints() ([]string, error) {
	return nil, fmt.Errorf("Can't list images from OCI registry")
}

// GetImagesWithFilter returns a filtered list of available images as Image structs.
func (r *ProtocolOCI) GetImagesWithFilter(filters []string) ([]api.Image, error) {
	return nil, fmt.Errorf("Can't list images from OCI registry")
}

// GetImage returns an Image struct for the provided fingerprint.
func (r *ProtocolOCI) GetImage(fingerprint string) (*api.Image, string, error) {
	info, ok := r.cache[fingerprint]
	if !ok {
		return nil, "", fmt.Errorf("Image not found")
	}

	img := api.Image{
		ImagePut: api.ImagePut{
			Public: true,
			Properties: map[string]string{
				"architecture": info.Architecture,
				"type":         "oci",
				"description":  fmt.Sprintf("%s (OCI)", info.Name),
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

	info, ok := r.cache[fingerprint]
	if !ok {
		return nil, fmt.Errorf("Image not found")
	}

	// Quick checks.
	if req.MetaFile == nil && req.RootfsFile == nil {
		return nil, fmt.Errorf("No file requested")
	}

	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("OCI image export currently requires root access")
	}

	// Get some temporary storage.
	ociPath, err := os.MkdirTemp("", "incus-oci-")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.RemoveAll(ociPath) }()

	err = os.Mkdir(filepath.Join(ociPath, "oci"), 0700)
	if err != nil {
		return nil, err
	}

	err = os.Mkdir(filepath.Join(ociPath, "image"), 0700)
	if err != nil {
		return nil, err
	}

	// Copy the image.
	if req.ProgressHandler != nil {
		req.ProgressHandler(ioprogress.ProgressData{Text: "Retrieving OCI image from registry"})
	}

	_, err = subprocess.RunCommand(
		"skopeo",
		"--insecure-policy",
		"copy",
		fmt.Sprintf("%s/%s", strings.Replace(r.httpHost, "https://", "docker://", -1), info.Alias),
		fmt.Sprintf("oci:%s:latest", filepath.Join(ociPath, "oci")))
	if err != nil {
		return nil, err
	}

	// Convert to something usable.
	if req.ProgressHandler != nil {
		req.ProgressHandler(ioprogress.ProgressData{Text: "Unpacking the OCI image"})
	}

	_, err = subprocess.RunCommand(
		"umoci",
		"unpack",
		"--keep-dirlinks",
		"--image", filepath.Join(ociPath, "oci"),
		filepath.Join(ociPath, "image"))
	if err != nil {
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

	err = os.WriteFile(filepath.Join(ociPath, "image", "metadata.yaml"), data, 0644)
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
func (r *ProtocolOCI) GetImageSecret(fingerprint string) (string, error) {
	return "", fmt.Errorf("Private images aren't supported with OCI registry")
}

// GetPrivateImage isn't relevant for the simplestreams protocol.
func (r *ProtocolOCI) GetPrivateImage(fingerprint string, secret string) (*api.Image, string, error) {
	return nil, "", fmt.Errorf("Private images aren't supported with OCI registry")
}

// GetPrivateImageFile isn't relevant for the simplestreams protocol.
func (r *ProtocolOCI) GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (*ImageFileResponse, error) {
	return nil, fmt.Errorf("Private images aren't supported with OCI registry")
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs.
func (r *ProtocolOCI) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	return nil, fmt.Errorf("Can't list image aliases from OCI registry")
}

// GetImageAliasNames returns the list of available alias names.
func (r *ProtocolOCI) GetImageAliasNames() ([]string, error) {
	return nil, fmt.Errorf("Can't list image aliases from OCI registry")
}

// GetImageAlias returns an existing alias as an ImageAliasesEntry struct.
func (r *ProtocolOCI) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	// Get the image information from skopeo.
	stdout, err := subprocess.RunCommand("skopeo", "inspect", fmt.Sprintf("%s/%s", strings.Replace(r.httpHost, "https://", "docker://", -1), name))
	if err != nil {
		return nil, "", err
	}

	// Parse the image info.
	var info ociInfo
	err = json.Unmarshal([]byte(stdout), &info)
	if err != nil {
		return nil, "", err
	}

	info.Alias = name
	info.Digest = strings.Replace(info.Digest, "sha256:", "", -1)

	archID, err := osarch.ArchitectureId(info.Architecture)
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
		return nil, "", fmt.Errorf("OCI images are only supported for containers")
	}

	return r.GetImageAlias(name)
}

// GetImageAliasArchitectures returns a map of architectures / targets.
func (r *ProtocolOCI) GetImageAliasArchitectures(imageType string, name string) (map[string]*api.ImageAliasesEntry, error) {
	if api.InstanceType(imageType) == api.InstanceTypeVM {
		return nil, fmt.Errorf("OCI images are only supported for containers")
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
func (r *ProtocolOCI) ExportImage(fingerprint string, image api.ImageExportPost) (Operation, error) {
	return nil, fmt.Errorf("Exporting images is not supported with OCI registry")
}
