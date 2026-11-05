//go:build !linux

package incus

import (
	"fmt"
)

func (r *ProtocolOCI) unpackOCIImage(imagePath string, imageTag string, bundlePath string, maxSize int64) error {
	return fmt.Errorf("Platform isn't supported")
}
