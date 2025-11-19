package linux

/*
#include <linux/btrfs.h>
#include <linux/hidraw.h>
#include <linux/vhost.h>

#define ZFS_MAX_DATASET_NAME_LEN 256
#define BLKZNAME _IOR(0x12, 125, char[ZFS_MAX_DATASET_NAME_LEN])
*/
import "C"

const (
	// IoctlBtrfsSetReceivedSubvol matches BTRFS_IOC_SET_RECEIVED_SUBVOL.
	IoctlBtrfsSetReceivedSubvol = C.BTRFS_IOC_SET_RECEIVED_SUBVOL

	// IoctlHIDIOCGrawInfo matches HIDIOCGRAWINFO.
	IoctlHIDIOCGrawInfo = C.HIDIOCGRAWINFO

	// IoctlVhostVsockSetGuestCid matches VHOST_VSOCK_SET_GUEST_CID.
	IoctlVhostVsockSetGuestCid = C.VHOST_VSOCK_SET_GUEST_CID

	// IoctlBlkZname matches BLKZNAME (ZFS specific).
	IoctlBlkZname = C.BLKZNAME
)
