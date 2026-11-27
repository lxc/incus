package endpoints

import (
	"net"

	"github.com/mdlayher/vsock"

	"github.com/lxc/incus/v7/internal/server/endpoints/listeners"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

func createVsockListener(cert *localtls.CertInfo) (net.Listener, error) {
	// Let the kernel pick a port from the full 32-bit range, which a guest can't exhaust.
	listener, err := vsock.ListenContextID(vsock.Host, 0, nil)
	if err != nil {
		return nil, err
	}

	return listeners.NewFancyTLSListener(listener, cert), nil
}

// VsockAddress returns the network address of the vsock endpoint, or nil if there's no vsock endpoint.
func (e *Endpoints) VsockAddress() net.Addr {
	e.mu.RLock()
	defer e.mu.RUnlock()

	listener := e.listeners[vmvsock]
	if listener == nil {
		return nil
	}

	return listener.Addr()
}
