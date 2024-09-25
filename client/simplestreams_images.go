package incus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
	"github.com/lxc/incus/v6/shared/util"
)

// Image handling functions

// GetImages returns a list of available images as Image structs.
func (r *ProtocolSimpleStreams) GetImages() ([]api.Image, error) {
	return r.ssClient.ListImages()
}

// GetImagesAllProjects returns a list of available images as Image structs.
func (r *ProtocolSimpleStreams) GetImagesAllProjects() ([]api.Image, error) {
	return r.GetImages()
}

// GetImageFingerprints returns a list of available image fingerprints.
func (r *ProtocolSimpleStreams) GetImageFingerprints() ([]string, error) {
	// Get all the images from simplestreams
	images, err := r.ssClient.ListImages()
	if err != nil {
		return nil, err
	}

	// And now extract just the fingerprints
	fingerprints := []string{}
	for _, img := range images {
		fingerprints = append(fingerprints, img.Fingerprint)
	}

	return fingerprints, nil
}

// GetImagesWithFilter returns a filtered list of available images as Image structs.
func (r *ProtocolSimpleStreams) GetImagesWithFilter(filters []string) ([]api.Image, error) {
	return nil, fmt.Errorf("GetImagesWithFilter is not supported by the simplestreams protocol")
}

// GetImage returns an Image struct for the provided fingerprint.
func (r *ProtocolSimpleStreams) GetImage(fingerprint string) (*api.Image, string, error) {
	image, err := r.ssClient.GetImage(fingerprint)
	if err != nil {
		return nil, "", fmt.Errorf("Failed getting image: %w", err)
	}

	return image, "", err
}

// GetImageFile downloads an image from the server, returning an ImageFileResponse struct.
func (r *ProtocolSimpleStreams) GetImageFile(fingerprint string, req ImageFileRequest) (*ImageFileResponse, error) {
	// Quick checks.
	if req.MetaFile == nil && req.RootfsFile == nil {
		return nil, fmt.Errorf("No file requested")
	}

	// Attempt to download from host
	if util.PathExists("/dev/incus/sock") && os.Geteuid() == 0 {
		unixURI := fmt.Sprintf("http://unix.socket/1.0/images/%s/export", url.PathEscape(fingerprint))

		// Setup the HTTP client
		devIncusHTTP, err := unixHTTPClient(nil, "/dev/incus/sock")
		if err == nil {
			resp, err := incusDownloadImage(fingerprint, unixURI, r.httpUserAgent, devIncusHTTP.Do, req)
			if err == nil {
				return resp, nil
			}
		}
	}

	// Use relatively short response header timeout so as not to hold the image lock open too long.
	// Deference client and transport in order to clone them so as to not modify timeout of base client.
	httpClient := *r.http
	httpTransport := httpClient.Transport.(*http.Transport).Clone()
	httpTransport.ResponseHeaderTimeout = 30 * time.Second
	httpClient.Transport = httpTransport

	// Get the file list
	files, err := r.ssClient.GetFiles(fingerprint)
	if err != nil {
		return nil, err
	}

	// Prepare the response
	resp := ImageFileResponse{}

	// Download function
	download := func(path string, filename string, hash string, target io.WriteSeeker) (int64, error) {
		// Try over http
		uri, err := url.JoinPath(fmt.Sprintf("http://%s", strings.TrimPrefix(r.httpHost, "https://")), path)
		if err != nil {
			return -1, err
		}

		size, err := util.DownloadFileHash(context.TODO(), &httpClient, r.httpUserAgent, req.ProgressHandler, req.Canceler, filename, uri, hash, sha256.New(), target)
		if err != nil {
			// Handle cancelation
			if err.Error() == "net/http: request canceled" {
				return -1, err
			}

			// Try over https
			uri, err := url.JoinPath(r.httpHost, path)
			if err != nil {
				return -1, err
			}

			size, err = util.DownloadFileHash(context.TODO(), &httpClient, r.httpUserAgent, req.ProgressHandler, req.Canceler, filename, uri, hash, sha256.New(), target)
			if err != nil {
				return -1, err
			}
		}

		return size, nil
	}

	// Download the Incus image file
	meta, ok := files["meta"]
	if ok && req.MetaFile != nil {
		size, err := download(meta.Path, "metadata", meta.Sha256, req.MetaFile)
		if err != nil {
			return nil, err
		}

		parts := strings.Split(meta.Path, "/")
		resp.MetaName = parts[len(parts)-1]
		resp.MetaSize = size
	}

	// Download the rootfs
	rootfs, ok := files["root"]
	if ok && req.RootfsFile != nil {
		// Look for deltas (requires xdelta3)
		downloaded := false
		_, err := exec.LookPath("xdelta3")
		if err == nil && req.DeltaSourceRetriever != nil {
			for filename, file := range files {
				_, srcFingerprint, prefixFound := strings.Cut(filename, "root.delta-")
				if !prefixFound {
					continue
				}

				// Check if we have the source file for the delta
				srcPath := req.DeltaSourceRetriever(srcFingerprint, "rootfs")
				if srcPath == "" {
					continue
				}

				// Create temporary file for the delta
				deltaFile, err := os.CreateTemp("", "incus_image_")
				if err != nil {
					return nil, err
				}

				defer func() { _ = deltaFile.Close() }()

				defer func() { _ = os.Remove(deltaFile.Name()) }()

				// Download the delta
				_, err = download(file.Path, "rootfs delta", file.Sha256, deltaFile)
				if err != nil {
					return nil, err
				}

				// Create temporary file for the delta
				patchedFile, err := os.CreateTemp("", "incus_image_")
				if err != nil {
					return nil, err
				}

				defer func() { _ = patchedFile.Close() }()

				defer func() { _ = os.Remove(patchedFile.Name()) }()

				// Apply it
				_, err = subprocess.RunCommand("xdelta3", "-f", "-d", "-s", srcPath, deltaFile.Name(), patchedFile.Name())
				if err != nil {
					return nil, err
				}

				// Copy to the target
				size, err := io.Copy(req.RootfsFile, patchedFile)
				if err != nil {
					return nil, err
				}

				parts := strings.Split(rootfs.Path, "/")
				resp.RootfsName = parts[len(parts)-1]
				resp.RootfsSize = size
				downloaded = true
			}
		}

		// Download the whole file
		if !downloaded {
			size, err := download(rootfs.Path, "rootfs", rootfs.Sha256, req.RootfsFile)
			if err != nil {
				return nil, err
			}

			parts := strings.Split(rootfs.Path, "/")
			resp.RootfsName = parts[len(parts)-1]
			resp.RootfsSize = size
		}
	}

	return &resp, nil
}

// GetImageSecret isn't relevant for the simplestreams protocol.
func (r *ProtocolSimpleStreams) GetImageSecret(fingerprint string) (string, error) {
	return "", fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetPrivateImage isn't relevant for the simplestreams protocol.
func (r *ProtocolSimpleStreams) GetPrivateImage(fingerprint string, secret string) (*api.Image, string, error) {
	return nil, "", fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetPrivateImageFile isn't relevant for the simplestreams protocol.
func (r *ProtocolSimpleStreams) GetPrivateImageFile(fingerprint string, secret string, req ImageFileRequest) (*ImageFileResponse, error) {
	return nil, fmt.Errorf("Private images aren't supported by the simplestreams protocol")
}

// GetImageAliases returns the list of available aliases as ImageAliasesEntry structs.
func (r *ProtocolSimpleStreams) GetImageAliases() ([]api.ImageAliasesEntry, error) {
	return r.ssClient.ListAliases()
}

// GetImageAliasNames returns the list of available alias names.
func (r *ProtocolSimpleStreams) GetImageAliasNames() ([]string, error) {
	// Get all the images from simplestreams
	aliases, err := r.ssClient.ListAliases()
	if err != nil {
		return nil, err
	}

	// And now extract just the names
	names := []string{}
	for _, alias := range aliases {
		names = append(names, alias.Name)
	}

	return names, nil
}

// GetImageAlias returns an existing alias as an ImageAliasesEntry struct.
func (r *ProtocolSimpleStreams) GetImageAlias(name string) (*api.ImageAliasesEntry, string, error) {
	alias, err := r.ssClient.GetAlias("container", name)
	if err != nil {
		alias, err = r.ssClient.GetAlias("virtual-machine", name)
		if err != nil {
			return nil, "", err
		}
	}

	return alias, "", err
}

// GetImageAliasType returns an existing alias as an ImageAliasesEntry struct.
func (r *ProtocolSimpleStreams) GetImageAliasType(imageType string, name string) (*api.ImageAliasesEntry, string, error) {
	if imageType == "" {
		return r.GetImageAlias(name)
	}

	alias, err := r.ssClient.GetAlias(imageType, name)
	if err != nil {
		return nil, "", err
	}

	return alias, "", err
}

// GetImageAliasArchitectures returns a map of architectures / targets.
func (r *ProtocolSimpleStreams) GetImageAliasArchitectures(imageType string, name string) (map[string]*api.ImageAliasesEntry, error) {
	if imageType == "" {
		aliases, err := r.ssClient.GetAliasArchitectures("container", name)
		if err != nil {
			aliases, err = r.ssClient.GetAliasArchitectures("virtual-machine", name)
			if err != nil {
				return nil, err
			}
		}

		return aliases, nil
	}

	return r.ssClient.GetAliasArchitectures(imageType, name)
}

// ExportImage exports (copies) an image to a remote server.
func (r *ProtocolSimpleStreams) ExportImage(fingerprint string, image api.ImageExportPost) (Operation, error) {
	return nil, fmt.Errorf("Exporting images is not supported by the simplestreams protocol")
}
