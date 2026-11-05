//go:build linux

package incus

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/apex/log"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/cas/dir"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/opencontainers/umoci/oci/casext/blobcompress"
	"github.com/opencontainers/umoci/oci/casext/mediatype"
	"github.com/opencontainers/umoci/oci/layer"

	"github.com/lxc/incus/v6/shared/logger"
)

func init() {
	// apex/log is only used by umoci within Incus.
	// So configure its logger to forward to our logger with the relevant prefix.

	// Set the custom handler.
	log.SetHandler(&umociLogHandler{Message: "Unpacking OCI image"})
}

// Custom handler to intercept logs.
type umociLogHandler struct {
	Message string
}

// HandleLog implements a proxy between apex/log and our logger.
func (h *umociLogHandler) HandleLog(e *log.Entry) error {
	switch e.Level {
	case log.DebugLevel:
		logger.Debug(h.Message, logger.Ctx{"log": e.Message})
	case log.InfoLevel:
		logger.Info(h.Message, logger.Ctx{"log": e.Message})
	case log.WarnLevel:
		logger.Warn(h.Message, logger.Ctx{"log": e.Message})
	case log.ErrorLevel:
		logger.Error(h.Message, logger.Ctx{"log": e.Message})
	case log.FatalLevel:
		logger.Panic(h.Message, logger.Ctx{"log": e.Message})
	default:
		logger.Error("Unknown umoci log level", logger.Ctx{"log": e.Message})
	}

	return nil
}

func (r *ProtocolOCI) unpackOCIImage(imagePath string, imageTag string, bundlePath string, maxSize int64) error {
	var unpackOptions layer.UnpackOptions
	unpackOptions.KeepDirlinks = true

	// Get a reference to the CAS.
	engine, err := dir.Open(imagePath)
	if err != nil {
		return fmt.Errorf("Open CAS: %w", err)
	}

	engineExt := casext.NewEngine(engine)
	defer func() { _ = engine.Close() }()

	// The layers decide how much gets written, check that before unpacking.
	if maxSize > 0 {
		err = checkUnpackedSize(engineExt, imageTag, maxSize)
		if err != nil {
			return err
		}
	}

	err = umoci.Unpack(engineExt, imageTag, bundlePath, unpackOptions)
	if err != nil {
		return err
	}

	return r.stripVolumeMounts(engineExt, imageTag, bundlePath)
}

// checkUnpackedSize fails if the image's layers unpack to more than maxSize.
func checkUnpackedSize(engineExt casext.Engine, imageTag string, maxSize int64) error {
	ctx := context.Background()

	descriptorPaths, err := engineExt.ResolveReference(ctx, imageTag)
	if err != nil {
		return fmt.Errorf("Resolve image reference: %w", err)
	}

	if len(descriptorPaths) != 1 {
		return fmt.Errorf("Bad number of descriptors for tag %q: %d", imageTag, len(descriptorPaths))
	}

	manifestBlob, err := engineExt.FromDescriptor(ctx, descriptorPaths[0].Descriptor())
	if err != nil {
		return fmt.Errorf("Get image manifest: %w", err)
	}

	defer logger.WarnOnError(manifestBlob.Close, "Failed to close manifest blob")

	manifest, ok := manifestBlob.Data.(ispec.Manifest)
	if !ok {
		return fmt.Errorf("Unexpected manifest media type %q", manifestBlob.Descriptor.MediaType)
	}

	var size int64
	for _, descriptor := range manifest.Layers {
		size, err = layerUnpackedSize(engineExt, descriptor, size, maxSize)
		if err != nil {
			return err
		}
	}

	return nil
}

// layerUnpackedSize adds the unpacked size of a layer to size, failing once it passes maxSize.
func layerUnpackedSize(engineExt casext.Engine, descriptor ispec.Descriptor, size int64, maxSize int64) (int64, error) {
	layerBlob, err := engineExt.FromDescriptor(context.Background(), descriptor)
	if err != nil {
		return -1, fmt.Errorf("Get image layer: %w", err)
	}

	defer logger.WarnOnError(layerBlob.Close, "Failed to close layer blob")

	layerData, ok := layerBlob.Data.(io.Reader)
	if !ok {
		return -1, fmt.Errorf("Unexpected layer media type %q", descriptor.MediaType)
	}

	// Decompress the layer the way umoci does.
	_, compression := mediatype.SplitMediaTypeSuffix(descriptor.MediaType)

	algorithm := blobcompress.GetAlgorithm(compression)
	if algorithm == nil {
		return -1, fmt.Errorf("Unsupported layer media type %q", descriptor.MediaType)
	}

	layerRaw, err := algorithm.Decompress(layerData)
	if err != nil {
		return -1, fmt.Errorf("Decompress image layer: %w", err)
	}

	defer logger.WarnOnError(layerRaw.Close, "Failed to close layer reader")

	tr := tar.NewReader(layerRaw)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return -1, fmt.Errorf("Read image layer: %w", err)
		}

		// Count a block per entry so directories and empty files aren't free.
		size += hdr.Size + 4096
		if size > maxSize {
			return -1, fmt.Errorf("Unpacked image size exceeds allowed budget of %d", maxSize)
		}
	}

	return size, nil
}

// stripVolumeMounts removes the tmpfs mounts umoci synthesizes from the image's volumes so those paths stay on the instance root disk.
func (r *ProtocolOCI) stripVolumeMounts(engineExt casext.Engine, imageTag string, bundlePath string) error {
	ctx := context.Background()

	// Get the image configuration.
	descriptorPaths, err := engineExt.ResolveReference(ctx, imageTag)
	if err != nil {
		return fmt.Errorf("Resolve image reference: %w", err)
	}

	if len(descriptorPaths) != 1 {
		return fmt.Errorf("Bad number of descriptors for tag %q: %d", imageTag, len(descriptorPaths))
	}

	manifestBlob, err := engineExt.FromDescriptor(ctx, descriptorPaths[0].Descriptor())
	if err != nil {
		return fmt.Errorf("Get image manifest: %w", err)
	}

	defer logger.WarnOnError(manifestBlob.Close, "Failed to close manifest blob")

	manifest, ok := manifestBlob.Data.(ispec.Manifest)
	if !ok {
		return fmt.Errorf("Unexpected manifest media type %q", manifestBlob.Descriptor.MediaType)
	}

	configBlob, err := engineExt.FromDescriptor(ctx, manifest.Config)
	if err != nil {
		return fmt.Errorf("Get image configuration: %w", err)
	}

	defer logger.WarnOnError(configBlob.Close, "Failed to close configuration blob")

	image, ok := configBlob.Data.(ispec.Image)
	if !ok {
		return fmt.Errorf("Unexpected configuration media type %q", configBlob.Descriptor.MediaType)
	}

	if len(image.Config.Volumes) == 0 {
		return nil
	}

	volumes := make(map[string]bool, len(image.Config.Volumes))
	for volume := range image.Config.Volumes {
		volumes[filepath.Clean(volume)] = true
	}

	// Filter the volume mounts out of the runtime specification.
	configPath := filepath.Join(bundlePath, "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var spec rspec.Spec
	err = json.Unmarshal(data, &spec)
	if err != nil {
		return err
	}

	mounts := make([]rspec.Mount, 0, len(spec.Mounts))
	for _, mount := range spec.Mounts {
		if mount.Type == "tmpfs" && volumes[filepath.Clean(mount.Destination)] {
			continue
		}

		mounts = append(mounts, mount)
	}

	spec.Mounts = mounts

	data, err = json.Marshal(&spec)
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0o644)
}
