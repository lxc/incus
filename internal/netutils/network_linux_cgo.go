//go:build linux && cgo

package netutils

/*
#include "unixfd.h"
#include "netns_getifaddrs.c"
*/
import "C"

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"unsafe"

	"github.com/lxc/incus/v6/shared/api"
)

// Allow the caller to set expectations.

// UnixFdsAcceptExact will only succeed if the exact amount of fds has been
// received  (unless combined with UNIX_FDS_ACCEPT_NONE).
const UnixFdsAcceptExact uint = C.UNIX_FDS_ACCEPT_EXACT

// UnixFdsAcceptLess will also succeed if less than the requested number of fd
// has been received.
// If the UNIX_FDS_ACCEPT_NONE flag is not raised than at least one fd must be
// received.
const UnixFdsAcceptLess uint = C.UNIX_FDS_ACCEPT_LESS

// UnixFdsAcceptMore will also succeed if more than the requested number of fds
// have been received. Any additional fds will be silently closed.
// If the UNIX_FDS_ACCEPT_NONE flag is not raised than at least one fd must be
// received.
const UnixFdsAcceptMore uint = C.UNIX_FDS_ACCEPT_MORE

// UnixFdsAcceptNone can be specified with any of the above flags and indicates
// that the caller will accept no file descriptors to be received.
const UnixFdsAcceptNone uint = C.UNIX_FDS_ACCEPT_NONE

// UnixFdsAcceptMask is the value of all the above flags or-ed together.
const UnixFdsAcceptMask uint = C.UNIX_FDS_ACCEPT_MASK

// Allow the callee to report back what happened. Only one of those will ever
// be set.

// UnixFdsReceivedExact indicates that the exact number of fds was received.
const UnixFdsReceivedExact uint = C.UNIX_FDS_RECEIVED_EXACT

// UnixFdsReceivedLess indicates that less than the requested number of fd has
// been received.
const UnixFdsReceivedLess uint = C.UNIX_FDS_RECEIVED_LESS

// UnixFdsReceivedMore indicates that more than the requested number of fd has
// been received.
const UnixFdsReceivedMore uint = C.UNIX_FDS_RECEIVED_MORE

// UnixFdsReceivedNone indicates that no fds have been received.
const UnixFdsReceivedNone uint = C.UNIX_FDS_RECEIVED_NONE

// NetnsGetifaddrs returns a map of InstanceStateNetwork for a particular process.
func NetnsGetifaddrs(initPID int32, hostInterfaces []net.Interface) (map[string]api.InstanceStateNetwork, error) {
	var netnsidAware C.bool
	var ifaddrs *C.struct_netns_ifaddrs
	var netnsID C.__s32

	if initPID > 0 {
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", initPID))
		if err != nil {
			return nil, err
		}

		defer func() { _ = f.Close() }()

		netnsID = C.netns_get_nsid(C.__s32(f.Fd()))
		if netnsID < 0 {
			return nil, fmt.Errorf("Failed to retrieve network namespace id")
		}
	} else {
		netnsID = -1
	}

	ret := C.netns_getifaddrs(&ifaddrs, netnsID, &netnsidAware)
	if ret < 0 {
		return nil, fmt.Errorf("Failed to retrieve network interfaces and addresses")
	}

	defer C.netns_freeifaddrs(ifaddrs)

	if netnsID >= 0 && !netnsidAware {
		return nil, fmt.Errorf("Netlink requests are not fully network namespace id aware")
	}

	// We're using the interface name as key here but we should really
	// switch to the ifindex at some point to handle ip aliasing correctly.
	networks := map[string]api.InstanceStateNetwork{}

	for addr := ifaddrs; addr != nil; addr = addr.ifa_next {
		var address [C.INET6_ADDRSTRLEN]C.char
		addNetwork, networkExists := networks[C.GoString(addr.ifa_name)]
		if !networkExists {
			addNetwork = api.InstanceStateNetwork{
				Addresses: []api.InstanceStateNetworkAddress{},
				Counters:  api.InstanceStateNetworkCounters{},
			}
		}

		// Interface flags
		netState := "down"
		netType := "unknown"

		if (addr.ifa_flags & C.IFF_BROADCAST) > 0 {
			netType = "broadcast"
		}

		if (addr.ifa_flags & C.IFF_LOOPBACK) > 0 {
			netType = "loopback"
		}

		if (addr.ifa_flags & C.IFF_POINTOPOINT) > 0 {
			netType = "point-to-point"
		}

		if (addr.ifa_flags & C.IFF_UP) > 0 {
			netState = "up"
		}

		addNetwork.State = netState
		addNetwork.Type = netType
		addNetwork.Mtu = int(addr.ifa_mtu)

		if initPID != 0 && int(addr.ifa_ifindex_peer) > 0 {
			for _, hostInterface := range hostInterfaces {
				if hostInterface.Index == int(addr.ifa_ifindex_peer) {
					addNetwork.HostName = hostInterface.Name
					break
				}
			}
		}

		// Addresses
		if addr.ifa_addr != nil && (addr.ifa_addr.sa_family == C.AF_INET || addr.ifa_addr.sa_family == C.AF_INET6) {
			family := "inet"
			if addr.ifa_addr.sa_family == C.AF_INET6 {
				family = "inet6"
			}

			addrPtr := C.get_addr_ptr(addr.ifa_addr)
			if addrPtr == nil {
				return nil, fmt.Errorf("Failed to retrieve valid address pointer")
			}

			addressStr := C.inet_ntop(C.int(addr.ifa_addr.sa_family), addrPtr, &address[0], C.INET6_ADDRSTRLEN)
			if addressStr == nil {
				return nil, fmt.Errorf("Failed to retrieve address string")
			}

			if addNetwork.Addresses == nil {
				addNetwork.Addresses = []api.InstanceStateNetworkAddress{}
			}

			goAddrString := C.GoString(addressStr)
			scope := "global"
			if strings.HasPrefix(goAddrString, "127") {
				scope = "local"
			}

			if goAddrString == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(goAddrString, "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(goAddrString, "fe80:") {
				scope = "link"
			}

			address := api.InstanceStateNetworkAddress{}
			address.Family = family
			address.Address = goAddrString
			address.Netmask = fmt.Sprintf("%d", int(addr.ifa_prefixlen))
			address.Scope = scope

			addNetwork.Addresses = append(addNetwork.Addresses, address)
		} else if addr.ifa_addr != nil && addr.ifa_addr.sa_family == C.AF_PACKET {
			if (addr.ifa_flags & C.IFF_LOOPBACK) == 0 {
				var buf [1024]C.char

				hwaddr := C.get_packet_address(addr.ifa_addr, &buf[0], 1024)
				if hwaddr == nil {
					return nil, fmt.Errorf("Failed to retrieve hardware address")
				}

				addNetwork.Hwaddr = C.GoString(hwaddr)
			}
		}

		if addr.ifa_stats_type == C.IFLA_STATS64 {
			addNetwork.Counters.BytesReceived = int64(addr.ifa_stats64.rx_bytes)
			addNetwork.Counters.BytesSent = int64(addr.ifa_stats64.tx_bytes)
			addNetwork.Counters.PacketsReceived = int64(addr.ifa_stats64.rx_packets)
			addNetwork.Counters.PacketsSent = int64(addr.ifa_stats64.tx_packets)
			addNetwork.Counters.ErrorsReceived = int64(addr.ifa_stats64.rx_errors)
			addNetwork.Counters.ErrorsSent = int64(addr.ifa_stats64.tx_errors)
			addNetwork.Counters.PacketsDroppedInbound = int64(addr.ifa_stats64.rx_dropped)
			addNetwork.Counters.PacketsDroppedOutbound = int64(addr.ifa_stats64.tx_dropped)
		}

		ifName := C.GoString(addr.ifa_name)

		networks[ifName] = addNetwork
	}

	return networks, nil
}

// AbstractUnixSendFd sends a Unix file descriptor over a Unix socket.
func AbstractUnixSendFd(sockFD int, sendFD int) error {
	fd := C.int(sendFD)
	skFd := C.int(sockFD)
	ret := C.lxc_abstract_unix_send_fds(skFd, &fd, C.int(1), nil, C.size_t(0))
	if ret < 0 {
		return fmt.Errorf("Failed to send file descriptor via abstract unix socket")
	}

	return nil
}

// AbstractUnixReceiveFd receives a Unix file descriptor from a Unix socket.
func AbstractUnixReceiveFd(sockFD int, flags uint) (*os.File, error) {
	skFd := C.int(sockFD)
	fds := C.struct_unix_fds{}
	fds.fd_count_max = 1
	fds.flags = C.__u32(flags)
	ret := C.lxc_abstract_unix_recv_fds(skFd, &fds, nil, C.size_t(0))
	if ret < 0 {
		return nil, fmt.Errorf("Failed to receive file descriptor via abstract unix socket")
	}

	if fds.fd_count_max != fds.fd_count_ret {
		return nil, fmt.Errorf("Failed to receive file descriptor via abstract unix socket")
	}

	file := os.NewFile(uintptr(fds.fd[0]), "")
	return file, nil
}

// AbstractUnixReceiveFdData is a low level function to receive a file descriptor over a unix socket.
func AbstractUnixReceiveFdData(sockFD int, numFds int, flags uint, iov unsafe.Pointer, iovLen int32) (uint64, []C.int, error) {
	fds := C.struct_unix_fds{}

	if numFds >= C.KERNEL_SCM_MAX_FD {
		return 0, []C.int{-C.EBADF}, fmt.Errorf("Excessive number of file descriptors requested")
	}

	fds.fd_count_max = C.__u32(numFds)
	fds.flags = C.__u32(flags)

	skFd := C.int(sockFD)
	ret, errno := C.lxc_abstract_unix_recv_fds_iov(skFd, &fds, (*C.struct_iovec)(iov), C.size_t(iovLen))
	if ret < 0 {
		return 0, []C.int{-C.EBADF}, fmt.Errorf("Failed to receive file descriptor via abstract unix socket: errno=%d", errno)
	}

	if ret == 0 {
		return 0, []C.int{-C.EBADF}, io.EOF
	}

	if fds.fd_count_ret == 0 {
		return 0, []C.int{-C.EBADF}, io.EOF
	}

	cfd := make([]C.int, numFds)

	// Transfer the file descriptors.
	for i := C.__u32(0); i < fds.fd_count_ret; i++ {
		cfd[i] = fds.fd[i]
	}

	// Make sure that when we received less fds than we intended any
	// additional entries are negative.
	for i := fds.fd_count_ret; i < C.__u32(numFds); i++ {
		cfd[i] = -1
	}

	return uint64(ret), cfd, nil
}
