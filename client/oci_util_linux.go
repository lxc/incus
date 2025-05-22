//go:build linux

package incus

import (
	"fmt"

	"github.com/apex/log"
	"github.com/opencontainers/umoci"
	"github.com/opencontainers/umoci/oci/cas/dir"
	"github.com/opencontainers/umoci/oci/casext"
	"github.com/opencontainers/umoci/oci/layer"

	"github.com/lxc/incus/v6/shared/logger"
)

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
	// Set the custom handler
	log.SetHandler(&umociLogHandler{Message: "Unpacking OCI image"})
	defer log.SetHandler(nil)

	var unpackOptions layer.UnpackOptions
	unpackOptions.KeepDirlinks = true

	// Get a reference to the CAS.
	engine, err := dir.Open(imagePath)
	if err != nil {
		return fmt.Errorf("Open CAS: %w", err)
	}

	engineExt := casext.NewEngine(engine)
	defer func() { _ = engine.Close() }()

	return umoci.Unpack(engineExt, imageTag, bundlePath, unpackOptions)
}
