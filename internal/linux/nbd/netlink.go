package nbd

import (
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

// Generic netlink interface of the kernel nbd driver.
const (
	attrIndex             = 1
	attrSizeBytes         = 2
	attrBlockSizeBytes    = 3
	attrServerFlags       = 5
	attrSockets           = 7
	attrBackendIdentifier = 10

	sockItem = 1
	sockFD   = 1

	cmdConnect    = 1
	cmdDisconnect = 2
)

// Connect attaches a negotiated connection to a kernel device and returns the device index.
// A negative index asks the kernel for an unused device, creating one if needed.
func Connect(conn *net.UnixConn, export *Export, backend string, index int) (int, error) {
	req, err := newRequest(cmdConnect)
	if err != nil {
		return -1, err
	}

	if index >= 0 {
		req.AddData(nl.NewRtAttr(attrIndex, nl.Uint32Attr(uint32(index))))
	}

	f, err := addExportAttrs(req, conn, export, backend)
	if err != nil {
		return -1, err
	}

	defer func() { _ = f.Close() }()

	msgs, err := req.Execute(unix.NETLINK_GENERIC, 0)
	if err != nil {
		return -1, fmt.Errorf("Failed attaching NBD device: %w", err)
	}

	for _, msg := range msgs {
		attrs, err := nl.ParseRouteAttr(msg[nl.SizeofGenlmsg:])
		if err != nil {
			return -1, err
		}

		for _, attr := range attrs {
			if attr.Attr.Type == attrIndex && len(attr.Value) >= 4 {
				return int(nl.NativeEndian().Uint32(attr.Value)), nil
			}
		}
	}

	return -1, errors.New("Kernel didn't report the NBD device index")
}

// Disconnect detaches the server from a device.
func Disconnect(index int) error {
	req, err := newRequest(cmdDisconnect)
	if err != nil {
		return err
	}

	req.AddData(nl.NewRtAttr(attrIndex, nl.Uint32Attr(uint32(index))))

	_, err = req.Execute(unix.NETLINK_GENERIC, 0)
	if err != nil {
		return fmt.Errorf("Failed detaching NBD device: %w", err)
	}

	return nil
}

// newRequest prepares a request for the nbd netlink family.
func newRequest(cmd uint8) (*nl.NetlinkRequest, error) {
	family, err := netlink.GenlFamilyGet("nbd")
	if err != nil {
		return nil, fmt.Errorf("Failed looking up the nbd netlink family: %w", err)
	}

	req := nl.NewNetlinkRequest(int(family.ID), unix.NLM_F_REQUEST|unix.NLM_F_ACK)
	req.AddData(&nl.Genlmsg{Command: cmd, Version: uint8(family.Version)})

	return req, nil
}

// addExportAttrs adds the export geometry, the backend identifier and the socket to the request.
// The returned descriptor must stay open until the request has been sent.
func addExportAttrs(req *nl.NetlinkRequest, conn *net.UnixConn, export *Export, backend string) (*os.File, error) {
	req.AddData(nl.NewRtAttr(attrSizeBytes, nl.Uint64Attr(export.Size)))
	req.AddData(nl.NewRtAttr(attrBlockSizeBytes, nl.Uint64Attr(export.BlockSize)))
	req.AddData(nl.NewRtAttr(attrServerFlags, nl.Uint64Attr(uint64(export.Flags))))
	req.AddData(nl.NewRtAttr(attrBackendIdentifier, nl.ZeroTerminated(backend)))

	// The kernel takes its own reference on the socket, the duplicate only has to live until the request is sent.
	f, err := conn.File()
	if err != nil {
		return nil, fmt.Errorf("Failed getting the NBD socket descriptor: %w", err)
	}

	sockets := nl.NewRtAttr(attrSockets|unix.NLA_F_NESTED, nil)
	item := sockets.AddRtAttr(sockItem|unix.NLA_F_NESTED, nil)
	item.AddRtAttr(sockFD, nl.Uint32Attr(uint32(f.Fd())))
	req.AddData(sockets)

	return f, nil
}
