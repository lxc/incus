//go:build linux

package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
	rspec "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/cas/dir"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/opencontainers/umoci/oci/layer"

	"github.com/lxc/incus/v7/shared/logger"
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

func (r *ProtocolOCI) unpackOCIImage(imagePath string, imageTag string, bundlePath string) error {
	var unpackOptions layer.UnpackOptions
	unpackOptions.KeepDirlinks = true

	// Get a reference to the CAS.
	engine, err := dir.Open(imagePath)
	if err != nil {
		return fmt.Errorf("Open CAS: %w", err)
	}

	engineExt := casext.NewEngine(engine)
	defer logger.WarnOnError(engine.Close, "Failed to close CAS engine")

	err = umoci.Unpack(engineExt, imageTag, bundlePath, unpackOptions)
	if err != nil {
		return err
	}

	return r.stripVolumeMounts(engineExt, imageTag, bundlePath)
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
