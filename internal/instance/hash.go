package instance

import (
	"crypto/sha256"
	"encoding/base64"
)

// HashName returns a hash of the name if it exceeds the given length limit.
// Otherwise, it returns the original name unchanged.
func HashName(value string, maxLength int) string {
	if len(value) > maxLength {
		// If the name is too long, hash it as SHA-256 (32 bytes).
		// Then encode the SHA-256 binary hash as Base64 Raw URL format and trim down to 'maxLength' chars.
		// Raw URL avoids the use of "+" character and the padding "=" character which QEMU doesn't allow.
		hash256 := sha256.New()
		hash256.Write([]byte(value))
		binaryHash := hash256.Sum(nil)
		value = base64.RawURLEncoding.EncodeToString(binaryHash)
		value = value[0:maxLength]
	}

	return value
}
