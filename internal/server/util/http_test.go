package util

import (
	"fmt"
)

func ExampleListenAddresses() {
	listenAddressConfigs := []string{
		"",
		"127.0.0.1:8000",   // Valid IPv4 address with port.
		"127.0.0.1",        // Valid IPv4 address without port.
		"[127.0.0.1]",      // Valid wrapped IPv4 address without port.
		"[::1]:8000",       // Valid IPv6 address with port.
		"::1:8000",         // Valid IPv6 address without port (that might look like a port).
		"::1",              // Valid IPv6 address without port.
		"[::1]",            // Valid wrapped IPv6 address without port.
		"example.com",      // Valid hostname without port.
		"example.com:8000", // Valid hostname with port.
		"foo:8000:9000",    // Invalid host and port combination.
		":::8000",          // Invalid host and port combination.
	}

	for _, listlistenAddressConfig := range listenAddressConfigs {
		listenAddress, err := ListenAddresses(listlistenAddressConfig)
		fmt.Printf("%q: %v %v\n", listlistenAddressConfig, listenAddress, err)
	}

	// Output: "": [] <nil>
	// "127.0.0.1:8000": [127.0.0.1:8000] <nil>
	// "127.0.0.1": [127.0.0.1:8443] <nil>
	// "[127.0.0.1]": [127.0.0.1:8443] <nil>
	// "[::1]:8000": [[::1]:8000] <nil>
	// "::1:8000": [[::1:8000]:8443] <nil>
	// "::1": [[::1]:8443] <nil>
	// "[::1]": [[::1]:8443] <nil>
	// "example.com": [example.com:8443] <nil>
	// "example.com:8000": [example.com:8000] <nil>
	// "foo:8000:9000": [] address foo:8000:9000: too many colons in address
	// ":::8000": [] address :::8000: too many colons in address
}
