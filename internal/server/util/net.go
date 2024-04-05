package util

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/lxc/incus/v6/internal/ports"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// InMemoryNetwork creates a fully in-memory listener and dial function.
//
// Each time the dial function is invoked a new pair of net.Conn objects will
// be created using net.Pipe: the listener's Accept method will unblock and
// return one end of the pipe and the other end will be returned by the dial
// function.
func InMemoryNetwork() (net.Listener, func() net.Conn) {
	listener := &inMemoryListener{
		conns:  make(chan net.Conn, 16),
		closed: make(chan struct{}),
	}

	dialer := func() net.Conn {
		server, client := net.Pipe()
		listener.conns <- server
		return client
	}

	return listener, dialer
}

type inMemoryListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

// Accept waits for and returns the next connection to the listener.
func (l *inMemoryListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, fmt.Errorf("closed")
	}
}

// Close closes the listener.
// Any blocked Accept operations will be unblocked and return errors.
func (l *inMemoryListener) Close() error {
	close(l.closed)
	return nil
}

// Addr returns the listener's network address.
func (l *inMemoryListener) Addr() net.Addr {
	return &inMemoryAddr{}
}

type inMemoryAddr struct {
}

func (a *inMemoryAddr) Network() string {
	return "memory"
}

func (a *inMemoryAddr) String() string {
	return ""
}

// ServerTLSConfig returns a new server-side tls.Config generated from the give
// certificate info.
func ServerTLSConfig(cert *localtls.CertInfo) *tls.Config {
	config := localtls.InitTLSConfig()
	config.ClientAuth = tls.RequestClientCert
	config.Certificates = []tls.Certificate{cert.KeyPair()}
	config.NextProtos = []string{"h2"} // Required by gRPC

	if cert.CA() != nil {
		pool := x509.NewCertPool()
		pool.AddCert(cert.CA())
		config.RootCAs = pool
		config.ClientCAs = pool

		logger.Infof("Incus is in CA mode, only CA-signed certificates will be allowed")
	}

	return config
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
	address1 = internalUtil.CanonicalNetworkAddress(address1, ports.HTTPSDefaultPort)
	address2 = internalUtil.CanonicalNetworkAddress(address2, ports.HTTPSDefaultPort)

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
	address = internalUtil.CanonicalNetworkAddress(address, ports.HTTPSDefaultPort)

	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}

	if host == "0.0.0.0" || host == "::" || host == "" {
		return true
	}

	return false
}

// SysctlGet retrieves the value of a sysctl file in /proc/sys.
func SysctlGet(path string) (string, error) {
	// Read the current content
	content, err := os.ReadFile(fmt.Sprintf("/proc/sys/%s", path))
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// SysctlSet writes a value to a sysctl file in /proc/sys.
// Requires an even number of arguments as key/value pairs. E.g. SysctlSet("path1", "value1", "path2", "value2").
func SysctlSet(parts ...string) error {
	partsLen := len(parts)
	if partsLen%2 != 0 {
		return fmt.Errorf("Requires even number of arguments")
	}

	for i := 0; i < partsLen; i = i + 2 {
		path := parts[i]
		newValue := parts[i+1]

		// Get current value.
		currentValue, err := SysctlGet(path)
		if err == nil && currentValue == newValue {
			// Nothing to update.
			return nil
		}

		err = os.WriteFile(fmt.Sprintf("/proc/sys/%s", path), []byte(newValue), 0)
		if err != nil {
			return err
		}
	}

	return nil
}
