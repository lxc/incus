package certificate

import (
	"fmt"

	"github.com/lxc/incus/shared/api"
)

// Type indicates the type of the certificate.
type Type int

// TypeClient indicates a client certificate type.
const TypeClient = Type(1)

// TypeServer indicates a server certificate type.
const TypeServer = Type(2)

// TypeMetrics indicates a metrics certificate type.
const TypeMetrics = Type(3)

// FromAPIType converts an API type to the equivalent Type.
func FromAPIType(apiType string) (Type, error) {
	switch apiType {
	case api.CertificateTypeClient:
		return TypeClient, nil
	case api.CertificateTypeServer:
		return TypeServer, nil
	case api.CertificateTypeMetrics:
		return TypeMetrics, nil
	}

	return -1, fmt.Errorf("Invalid certificate type")
}
