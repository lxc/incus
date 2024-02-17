package quota

/*
#include <linux/fs.h>
#include <linux/dqblk_xfs.h>
#include <linux/quota.h>
#include <sys/ioctl.h>
#include <sys/quota.h>
#include <sys/types.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdbool.h>
#include <unistd.h>
#include <errno.h>

#ifndef FS_XFLAG_PROJINHERIT
struct fsxattr {
	__u32		fsx_xflags;
	__u32		fsx_extsize;
	__u32		fsx_nextents;
	__u32		fsx_projid;
	unsigned char	fsx_pad[12];
};
#define FS_XFLAG_PROJINHERIT 0x00000200
#endif

#ifndef QIF_DQBLKSIZE_BITS
struct if_dqinfo {
	__u64 dqi_bgrace;
	__u64 dqi_igrace;
	__u32 dqi_flags;
	__u32 dqi_valid;
};

struct if_dqblk {
	__u64 dqb_bhardlimit;
	__u64 dqb_bsoftlimit;
	__u64 dqb_curspace;
	__u64 dqb_ihardlimit;
	__u64 dqb_isoftlimit;
	__u64 dqb_curinodes;
	__u64 dqb_btime;
	__u64 dqb_itime;
	__u32 dqb_valid;
};
#define QIF_DQBLKSIZE_BITS 10
#endif

#ifndef FS_IOC_FSGETXATTR
#define FS_IOC_FSGETXATTR _IOR ('X', 31, struct fsxattr)
#endif

#ifndef FS_IOC_FSSETXATTR
#define FS_IOC_FSSETXATTR _IOW ('X', 32, struct fsxattr)
#endif

#ifndef PRJQUOTA
#define PRJQUOTA 2
#endif

int quota_supported(char *dev_path) {
	struct if_dqinfo dqinfo;

	return quotactl(QCMD(Q_GETINFO, PRJQUOTA), dev_path, 0, (caddr_t)&dqinfo);
}

int64_t quota_get_usage(char *dev_path, uint32_t id) {
	struct if_dqblk quota;

	if (quotactl(QCMD(Q_GETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		return -1;
	}

	return quota.dqb_curspace;
}


int quota_set(char *dev_path, uint32_t id, uint64_t hard_bytes) {
	struct if_dqblk quota;
	fs_disk_quota_t xfsquota;

	if (quotactl(QCMD(Q_GETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		if (hard_bytes == 0 && errno == ENOENT)
			return 0;

		return -1;
	}

	quota.dqb_bhardlimit = hard_bytes;
	if (quotactl(QCMD(Q_SETQUOTA, PRJQUOTA), dev_path, id, (caddr_t)&quota) < 0) {
		xfsquota.d_version = FS_DQUOT_VERSION;
		xfsquota.d_id = id;
		xfsquota.d_flags = FS_PROJ_QUOTA;
		xfsquota.d_fieldmask = FS_DQ_BHARD;
		xfsquota.d_blk_hardlimit = hard_bytes * 1024 / 512;

		if (quotactl(QCMD(Q_XSETQLIM, PRJQUOTA), dev_path, id, (caddr_t)&xfsquota) < 0) {
			return -1;
		}
	}

	return 0;
}

int quota_set_path(char *path, uint32_t id, bool inherit) {
	struct fsxattr attr;
	int fd;
	int ret;

	fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;

	ret = ioctl(fd, FS_IOC_FSGETXATTR, &attr);
	if (ret < 0) {
		close(fd);
		return -1;
	}

	if (inherit) {
		attr.fsx_xflags |= FS_XFLAG_PROJINHERIT;
	}

	attr.fsx_projid = id;

	ret = ioctl(fd, FS_IOC_FSSETXATTR, &attr);
	if (ret < 0) {
		close(fd);
		return -1;
	}

	close(fd);
	return 0;
}

int32_t quota_get_path(char *path) {
	struct fsxattr attr;
	int fd;
	int ret;

	fd = open(path, O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return -1;

	ret = ioctl(fd, FS_IOC_FSGETXATTR, &attr);
	if (ret < 0) {
		close(fd);
		return -1;
	}

	close(fd);
	return attr.fsx_projid;
}

*/
import "C"

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/shared/util"
)

var errNoDevice = fmt.Errorf("Couldn't find backing device for mountpoint")

func devForPath(path string) (string, error) {
	// Get major/minor
	var stat unix.Stat_t
	err := unix.Lstat(path, &stat)
	if err != nil {
		return "", err
	}

	devMajor := unix.Major(uint64(stat.Dev))
	devMinor := unix.Minor(uint64(stat.Dev))

	// Parse mountinfo for it
	mountinfo, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}

	defer func() { _ = mountinfo.Close() }()

	scanner := bufio.NewScanner(mountinfo)
	for scanner.Scan() {
		line := scanner.Text()

		tokens := strings.Fields(line)
		if len(tokens) < 5 {
			continue
		}

		if tokens[2] == fmt.Sprintf("%d:%d", devMajor, devMinor) {
			if util.PathExists(tokens[len(tokens)-2]) {
				return tokens[len(tokens)-2], nil
			}
		}
	}

	return "", errNoDevice
}

// Supported check if the given path supports project quotas.
func Supported(path string) (bool, error) {
	// Get the backing device
	devPath, err := devForPath(path)
	if err != nil {
		return false, err
	}

	// Call quotactl through CGo
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	return C.quota_supported(cDevPath) == 0, nil
}

// GetProject returns the project quota ID for the given path.
func GetProject(path string) (uint32, error) {
	// Call ioctl through CGo
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	id := C.quota_get_path(cPath)
	if id < 0 {
		return 0, fmt.Errorf("Failed to get project from %q", path)
	}

	return uint32(id), nil
}

// SetProject recursively sets the project quota ID (and project inherit flag on directories) for the given path.
func SetProject(path string, id uint32) error {
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		inherit := false
		if info.IsDir() {
			inherit = true // Only can set FS_XFLAG_PROJINHERIT on directories.
		} else if !info.Mode().IsRegular() {
			// Cannot set project ID on non-regular files after file creation. Infact trying to set
			// project ID on some file types just blocks forever (such as pipe files).
			// So skip them as they don't take up disk space anyway.
			return nil
		}

		// Call ioctl through CGo.
		cPath := C.CString(filePath)
		defer C.free(unsafe.Pointer(cPath))

		if C.quota_set_path(cPath, C.uint32_t(id), C.bool(inherit)) != 0 {
			return fmt.Errorf(`Failed to set project ID "%d" on %q (inherit %t)`, id, filePath, inherit)
		}

		return nil
	})

	return err
}

// DeleteProject unsets the project id from the path and clears the quota for the project ID.
func DeleteProject(path string, id uint32) error {
	// Unset the project from the path.
	err := SetProject(path, 0)
	if err != nil {
		return err
	}

	// Unset the quota on the project.
	err = SetProjectQuota(path, id, 0)
	if err != nil {
		return err
	}

	return nil
}

// GetProjectUsage returns the current consumption.
func GetProjectUsage(path string, id uint32) (int64, error) {
	// Get the backing device.
	devPath, err := devForPath(path)
	if err != nil {
		return -1, err
	}

	// Call quotactl through CGo.
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	size := C.quota_get_usage(cDevPath, C.uint32_t(id))
	if size < 0 {
		return -1, fmt.Errorf(`Failed to get project consumption for ID "%d" on %q`, id, devPath)
	}

	return int64(size), nil
}

// SetProjectQuota sets the quota on the project ID.
func SetProjectQuota(path string, id uint32, bytes int64) error {
	// Get the backing device
	devPath, err := devForPath(path)
	if err != nil {
		return err
	}

	// Call quotactl through CGo
	cDevPath := C.CString(devPath)
	defer C.free(unsafe.Pointer(cDevPath))

	if C.quota_set(cDevPath, C.uint32_t(id), C.uint64_t(bytes/1024)) != 0 {
		return fmt.Errorf(`Failed to set project quota for ID "%d" on %q`, id, devPath)
	}

	return nil
}
