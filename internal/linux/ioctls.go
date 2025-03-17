package linux

/*
#include <linux/btrfs.h>
#include <linux/hidraw.h>
#include <linux/vhost.h>
*/
import "C"

const (
	IoctlBtrfsSetReceivedSubvol = C.BTRFS_IOC_SET_RECEIVED_SUBVOL
	IoctlHIDIOCGrawInfo         = C.HIDIOCGRAWINFO
	IoctlVhostVsockSetGuestCid  = C.VHOST_VSOCK_SET_GUEST_CID
)
