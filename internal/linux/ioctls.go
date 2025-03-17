package linux

/*
#include <linux/btrfs.h>
#include <linux/hidraw.h>
#include <linux/vhost.h>
*/
import "C"

const (
	// IoctlBtrfsSetReceivedSubvol matches BTRFS_IOC_SET_RECEIVED_SUBVOL.
	IoctlBtrfsSetReceivedSubvol = C.BTRFS_IOC_SET_RECEIVED_SUBVOL

	// IoctlHIDIOCGrawInfo matches HIDIOCGRAWINFO.
	IoctlHIDIOCGrawInfo = C.HIDIOCGRAWINFO

	// IoctlVhostVsockSetGuestCid matches VHOST_VSOCK_SET_GUEST_CID.
	IoctlVhostVsockSetGuestCid = C.VHOST_VSOCK_SET_GUEST_CID
)
