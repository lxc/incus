package util

import (
	"fmt"
	"net"

	"github.com/lxc/incus/v6/internal/ports"
)

// CanonicalNetworkAddress parses the given network address and returns a string of the form "host:port",
// possibly filling it with the default port if it's missing. It will also wrap a bare IPv6 address with square
// brackets if needed.
func CanonicalNetworkAddress(address string, defaultPort int) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		ip := net.ParseIP(address)
		if ip != nil {
			// If the input address is a bare IP address, then convert it to a proper listen address
			// using the canonical IP with default port and wrap IPv6 addresses in square brackets.
			address = net.JoinHostPort(ip.String(), fmt.Sprintf("%d", defaultPort))
		} else {
			// Otherwise assume this is either a host name or a partial address (e.g `[::]`) without
			// a port number, so append the default port.
			address = fmt.Sprintf("%s:%d", address, defaultPort)
		}
	} else if port == "" && address[len(address)-1] == ':' {
		// An address that ends with a trailing colon will be parsed as having an empty port.
		address = net.JoinHostPort(host, fmt.Sprintf("%d", defaultPort))
	}

	return address
}

// CanonicalNetworkAddressFromAddressAndPort returns a network address from separate address and port values.
// The address accepts values such as "[::]", "::" and "localhost".
func CanonicalNetworkAddressFromAddressAndPort(address string, port int, defaultPort int) string {
	// Because we accept just the host part of an IPv6 listen address (e.g. `[::]`) don't use net.JoinHostPort.
	// If a bare IP address is supplied then CanonicalNetworkAddress will use net.JoinHostPort if needed.
	return CanonicalNetworkAddress(fmt.Sprintf("%s:%d", address, port), defaultPort)
}

// NetworkInterfaceAddress returns the first global unicast address of any of the system network interfaces.
// Return the empty string if none is found.
func NetworkInterfaceAddress() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		if len(addrs) == 0 {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			if !ipNet.IP.IsGlobalUnicast() {
				continue
			}

			return ipNet.IP.String()
		}
	}

	return ""
}

// IsAddressCovered detects if network address1 is actually covered by
// address2, in the sense that they are either the same address or address2 is
// specified using a wildcard with the same port of address1.
func IsAddressCovered(address1, address2 string) bool {
	address1 = CanonicalNetworkAddress(address1, ports.HTTPSDefaultPort)
	address2 = CanonicalNetworkAddress(address2, ports.HTTPSDefaultPort)

	if address1 == address2 {
		return true
	}

	host1, port1, err := net.SplitHostPort(address1)
	if err != nil {
		return false
	}

	host2, port2, err := net.SplitHostPort(address2)
	if err != nil {
		return false
	}

	// If the ports are different, then address1 is clearly not covered by
	// address2.
	if port2 != port1 {
		return false
	}

	// If address1 contains a host name, let's try to resolve it, in order
	// to compare the actual IPs.
	var addresses1 []net.IP
	if host1 != "" {
		ip := net.ParseIP(host1)
		if ip != nil {
			addresses1 = append(addresses1, ip)
		} else {
			ips, err := net.LookupHost(host1)
			if err == nil && len(ips) > 0 {
				for _, ipStr := range ips {
					ip := net.ParseIP(ipStr)
					if ip != nil {
						addresses1 = append(addresses1, ip)
					}
				}
			}
		}
	}

	// If address2 contains a host name, let's try to resolve it, in order
	// to compare the actual IPs.
	var addresses2 []net.IP
	if host2 != "" {
		ip := net.ParseIP(host2)
		if ip != nil {
			addresses2 = append(addresses2, ip)
		} else {
			ips, err := net.LookupHost(host2)
			if err == nil && len(ips) > 0 {
				for _, ipStr := range ips {
					ip := net.ParseIP(ipStr)
					if ip != nil {
						addresses2 = append(addresses2, ip)
					}
				}
			}
		}
	}

	for _, a1 := range addresses1 {
		for _, a2 := range addresses2 {
			if a1.Equal(a2) {
				return true
			}
		}
	}

	// If address2 is using an IPv4 wildcard for the host, then address2 is
	// only covered if it's an IPv4 address.
	if host2 == "0.0.0.0" {
		ip1 := net.ParseIP(host1)
		if ip1 != nil && ip1.To4() != nil {
			return true
		}

		return false
	}

	// If address2 is using an IPv6 wildcard for the host, then address2 is
	// always covered.
	if host2 == "::" || host2 == "" {
		return true
	}

	return false
}

// IsWildCardAddress returns whether the given address is a wildcard.
func IsWildCardAddress(address string) bool {
	address = CanonicalNetworkAddress(address, ports.HTTPSDefaultPort)

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}

	if host == "0.0.0.0" || host == "::" || host == "" {
		return true
	}

	return false
}
