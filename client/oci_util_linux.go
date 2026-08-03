//go:build linux

package incus

import (
	"context"
	"fmt"

	"github.com/apex/log"
	ispec "github.com/opencontainers/image-spec/specs-go/v1"
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

func unpackOCIImage(imagePath string, imageTag string, bundlePath string) error {
	var unpackOptions layer.UnpackOptions
	unpackOptions.KeepDirlinks = true

	// Get a reference to the CAS.
	engine, err := dir.Open(imagePath)
	if err != nil {
		return fmt.Errorf("Open CAS: %w", err)
	}

	engineExt := casext.NewEngine(engine)
	defer logger.WarnOnError(engine.Close, "Failed to close CAS engine")

	return umoci.Unpack(engineExt, imageTag, bundlePath, unpackOptions)
}

// getOCIImageCmd reads the image config blob's Cmd field directly from a local OCI layout, the same way umoci itself reads it (no subprocess).
func getOCIImageCmd(imagePath string, imageTag string) ([]string, error) {
	engine, err := dir.Open(imagePath)
	if err != nil {
		return nil, fmt.Errorf("Open CAS: %w", err)
	}

	engineExt := casext.NewEngine(engine)
	defer logger.WarnOnError(engine.Close, "Failed to close CAS engine")

	ctx := context.Background()

	descriptorPaths, err := engineExt.ResolveReference(ctx, imageTag)
	if err != nil {
		return nil, fmt.Errorf("resolve reference: %w", err)
	}

	if len(descriptorPaths) != 1 {
		return nil, fmt.Errorf("tag is ambiguous or missing: %s", imageTag)
	}

	manifestBlob, err := engineExt.FromDescriptor(ctx, descriptorPaths[0].Descriptor())
	if err != nil {
		return nil, fmt.Errorf("get manifest: %w", err)
	}

	defer logger.WarnOnError(manifestBlob.Close, "Failed to close manifest blob")

	manifest, ok := manifestBlob.Data.(ispec.Manifest)
	if !ok {
		return nil, fmt.Errorf("unexpected manifest blob type: %s", manifestBlob.Descriptor.MediaType)
	}

	configBlob, err := engineExt.FromDescriptor(ctx, manifest.Config)
	if err != nil {
		return nil, fmt.Errorf("get config blob: %w", err)
	}

	defer logger.WarnOnError(configBlob.Close, "Failed to close config blob")

	config, ok := configBlob.Data.(ispec.Image)
	if !ok {
		return nil, fmt.Errorf("unexpected config blob type: %s", configBlob.Descriptor.MediaType)
	}

	return config.Config.Cmd, nil
}
