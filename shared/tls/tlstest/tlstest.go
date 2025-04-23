package tlstest

import (
	"testing"

	"github.com/lxc/incus/v6/shared/tls"
)

// TestingKeyPair returns CertInfo object initialized with a test keypair. It's
// meant to be used only by tests.
func TestingKeyPair(t *testing.T) *tls.CertInfo {
	cert, err := tls.KeyPairFromRaw(testCertPEMBlock, testKeyPEMBlock)
	if err != nil {
		t.Fatalf("invalid X509 keypair material: %v", err)
	}

	return cert
}

// TestingAltKeyPair returns CertInfo object initialized with a test keypair
// which differs from the one returned by TestCertInfo. It's meant to be used
// only by tests.
func TestingAltKeyPair(t *testing.T) *tls.CertInfo {
	cert, err := tls.KeyPairFromRaw(testAltCertPEMBlock, testAltKeyPEMBlock)
	if err != nil {
		t.Fatalf("invalid X509 keypair material: %v", err)
	}

	return cert
}

var testCertPEMBlock = []byte(`
-----BEGIN CERTIFICATE-----
MIIBzjCCAVSgAwIBAgIUJAEAVl1oOU+OQxj5aUrRdJDwuWEwCgYIKoZIzj0EAwMw
EzERMA8GA1UEAwwIYWx0LnRlc3QwHhcNMjIwNDEzMDQyMjA0WhcNMzIwNDEwMDQy
MjA0WjATMREwDwYDVQQDDAhhbHQudGVzdDB2MBAGByqGSM49AgEGBSuBBAAiA2IA
BGAmiHj98SXz0ZW1AxheW+zkFyPz5ZrZoZDY7NezGQpoH4KZ1x08X1jw67wv+M0c
W+yd2BThOcvItBO+HokJ03lgL6cgDojcmEEfZntgmGHjG7USqh48TrQtmt/uSJsD
4qNpMGcwHQYDVR0OBBYEFPOsHk3ewn4abmyzLgOXs3Bg8Dq9MB8GA1UdIwQYMBaA
FPOsHk3ewn4abmyzLgOXs3Bg8Dq9MA8GA1UdEwEB/wQFMAMBAf8wFAYDVR0RBA0w
C4IJbG9jYWxob3N0MAoGCCqGSM49BAMDA2gAMGUCMCKR+gWwN9VWXct8tDxCvlA6
+JP7iQPnLetiSLpyN4HEVQYP+EQhDJIJIy6+CwlUCQIxANQXfaTTrcVuhAb9dwVI
9bcu4cRGLEtbbNuOW/y+q7mXG0LtE/frDv/QrNpKhnnOzA==
-----END CERTIFICATE-----
`)

var testKeyPEMBlock = []byte(`
-----BEGIN PRIVATE KEY-----
MIG2AgEAMBAGByqGSM49AgEGBSuBBAAiBIGeMIGbAgEBBDBzlLjHjIxc5XHm95zB
p8cnUtHQcmdBy2Ekv+bbiaS/8M8Twp7Jvi47SruAY5gESK2hZANiAARgJoh4/fEl
89GVtQMYXlvs5Bcj8+Wa2aGQ2OzXsxkKaB+CmdcdPF9Y8Ou8L/jNHFvsndgU4TnL
yLQTvh6JCdN5YC+nIA6I3JhBH2Z7YJhh4xu1EqoePE60LZrf7kibA+I=
-----END PRIVATE KEY-----
`)

var testAltCertPEMBlock = []byte(`
-----BEGIN CERTIFICATE-----
MIIBzjCCAVSgAwIBAgIUK41+7aTdYLu3x3vGoDOqat10TmQwCgYIKoZIzj0EAwMw
EzERMA8GA1UEAwwIYWx0LnRlc3QwHhcNMjIwNDEzMDQyMzM0WhcNMzIwNDEwMDQy
MzM0WjATMREwDwYDVQQDDAhhbHQudGVzdDB2MBAGByqGSM49AgEGBSuBBAAiA2IA
BAHv2a3obPHcQVDQouW/A/M/l2xHUFINWvCIhA5gWCtj9RLWKD6veBR133qSr9w0
/DT96ZoTw7kJu/BQQFlRafmfMRTZcvXHLoPMoihBEkDqTGl2qwEQea/0MPi3thwJ
wqNpMGcwHQYDVR0OBBYEFKoF8yXx9lgBTQvZL2M8YqV4c4c5MB8GA1UdIwQYMBaA
FKoF8yXx9lgBTQvZL2M8YqV4c4c5MA8GA1UdEwEB/wQFMAMBAf8wFAYDVR0RBA0w
C4IJbG9jYWxob3N0MAoGCCqGSM49BAMDA2gAMGUCMQCcpYeYWmIL7QdUCGGRT8gt
YhQSciGzXlyncToAJ+A91dXGbGYvqfIti7R00sR+8cwCMAxglHP7iFzWrzn1M/Z9
H5bVDjnWZvsgEblThausOYxWxzxD+5dT5rItoVZOJhfPLw==
-----END CERTIFICATE-----
`)

var testAltKeyPEMBlock = []byte(`
-----BEGIN PRIVATE KEY-----
MIG2AgEAMBAGByqGSM49AgEGBSuBBAAiBIGeMIGbAgEBBDC3/Fv+SmNLfBy2AuUD
O3zHq1GMLvVfk3JkDIqqbKPJeEa2rS44bemExc8v85wVYTmhZANiAAQB79mt6Gzx
3EFQ0KLlvwPzP5dsR1BSDVrwiIQOYFgrY/US1ig+r3gUdd96kq/cNPw0/emaE8O5
CbvwUEBZUWn5nzEU2XL1xy6DzKIoQRJA6kxpdqsBEHmv9DD4t7YcCcI=
-----END PRIVATE KEY-----
`)
