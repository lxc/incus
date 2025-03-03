//go:build linux && cgo && !agent

package cluster

import "context"

// ImageGenerated is an interface of generated methods for Image.
type ImageGenerated interface {
	// GetImages returns all available images.
	// generator: image GetMany
	GetImages(ctx context.Context, db dbtx, filters ...ImageFilter) ([]Image, error)

	// GetImage returns the image with the given key.
	// generator: image GetOne
	GetImage(ctx context.Context, db dbtx, project string, fingerprint string) (*Image, error)
}
