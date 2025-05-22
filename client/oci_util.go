//go:build !linux

package incus

import (
	"fmt"
)

func unpackOCIImage(imagePath string, imageTag string, bundlePath string) error {
	return fmt.Errorf("Platform isn't supported")
}
