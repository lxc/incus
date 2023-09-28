#ifndef __INCUS_MOUNT_UTILS_H
#define __INCUS_MOUNT_UTILS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/sched.h>
#include <linux/types.h>
#include <sched.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

#include "compiler.h"
#include "syscall_numbers.h"

// open_tree() flags
#ifndef OPEN_TREE_CLONE
#define OPEN_TREE_CLONE 1
#endif

#ifndef OPEN_TREE_CLOEXEC
#define OPEN_TREE_CLOEXEC O_CLOEXEC
#endif

// fsmount() flags
#ifndef FSMOUNT_CLOEXEC
#define FSMOUNT_CLOEXEC 0x00000001
#endif

// mount attributes
#ifndef MOUNT_ATTR_RDONLY
#define MOUNT_ATTR_RDONLY 0x00000001 /* Mount read-only */
#endif

#ifndef MOUNT_ATTR_NOSUID
#define MOUNT_ATTR_NOSUID 0x00000002 /* Ignore suid and sgid bits */
#endif

#ifndef MOUNT_ATTR_NODEV
#define MOUNT_ATTR_NODEV 0x00000004 /* Disallow access to device special files */
#endif

#ifndef MOUNT_ATTR_NOEXEC
#define MOUNT_ATTR_NOEXEC 0x00000008 /* Disallow program execution */
#endif

#ifndef MOUNT_ATTR__ATIME
#define MOUNT_ATTR__ATIME 0x00000070 /* Setting on how atime should be updated */
#endif

#ifndef MOUNT_ATTR_RELATIME
#define MOUNT_ATTR_RELATIME 0x00000000 /* - Update atime relative to mtime/ctime. */
#endif

#ifndef MOUNT_ATTR_NOATIME
#define MOUNT_ATTR_NOATIME 0x00000010 /* - Do not update access times. */
#endif

#ifndef MOUNT_ATTR_STRICTATIME
#define MOUNT_ATTR_STRICTATIME 0x00000020 /* - Always perform atime updates */
#endif

#ifndef MOUNT_ATTR_NODIRATIME
#define MOUNT_ATTR_NODIRATIME 0x00000080 /* Do not update directory access times */
#endif

#ifndef MOUNT_ATTR_IDMAP
#define MOUNT_ATTR_IDMAP 0x00100000
#endif

// move_mount() flags
#ifndef MOVE_MOUNT_F_SYMLINKS
#define MOVE_MOUNT_F_SYMLINKS 0x00000001 /* Follow symlinks on from path */
#endif

#ifndef MOVE_MOUNT_F_AUTOMOUNTS
#define MOVE_MOUNT_F_AUTOMOUNTS 0x00000002 /* Follow automounts on from path */
#endif

#ifndef MOVE_MOUNT_F_EMPTY_PATH
#define MOVE_MOUNT_F_EMPTY_PATH 0x00000004 /* Empty from path permitted */
#endif

#ifndef MOVE_MOUNT_T_SYMLINKS
#define MOVE_MOUNT_T_SYMLINKS 0x00000010 /* Follow symlinks on to path */
#endif

#ifndef MOVE_MOUNT_T_AUTOMOUNTS
#define MOVE_MOUNT_T_AUTOMOUNTS 0x00000020 /* Follow automounts on to path */
#endif

#ifndef MOVE_MOUNT_T_EMPTY_PATH
#define MOVE_MOUNT_T_EMPTY_PATH 0x00000040 /* Empty to path permitted */
#endif

#ifndef MOVE_MOUNT__MASK
#define MOVE_MOUNT__MASK 0x00000077
#endif

#ifndef FSCONFIG_SET_STRING
#define FSCONFIG_SET_STRING 1 /* Set parameter, supplying a string value */
#endif

#ifndef FSCONFIG_CMD_CREATE
#define FSCONFIG_CMD_CREATE 6 /* Invoke superblock creation */
#endif

#ifndef FSOPEN_CLOEXEC
#define FSOPEN_CLOEXEC 0x00000001
#endif

static inline int incus_fsopen(const char *fs_name, unsigned int flags)
{
	return syscall(__NR_fsopen, fs_name, flags);
}

static inline int incus_fsconfig(int fd, unsigned int cmd, const char *key,
			       const void *value, int aux)
{
	return syscall(__NR_fsconfig, fd, cmd, key, value, aux);
}

static inline int incus_fsmount(int fs_fd, unsigned int flags,
			      unsigned int attr_flags)
{
	return syscall(__NR_fsmount, fs_fd, flags, attr_flags);
}

static inline int incus_open_tree(int dfd, const char *filename, unsigned int flags)
{
	return syscall(__NR_open_tree, dfd, filename, flags);
}

struct lxc_mount_attr {
	__u64 attr_set;
	__u64 attr_clr;
	__u64 propagation;
	__u64 userns_fd;
};

static inline int incus_mount_setattr(int dfd, const char *path, unsigned int flags,
				    struct lxc_mount_attr *attr, size_t size)
{
	return syscall(__NR_mount_setattr, dfd, path, flags, attr, size);
}

static inline int incus_move_mount(int from_dfd, const char *from_pathname,
				 int to_dfd, const char *to_pathname,
				 unsigned int flags)
{
	return syscall(__NR_move_mount, from_dfd, from_pathname, to_dfd,
		       to_pathname, flags);
}

#endif /* __INCUS_MOUNT_UTILS_H */

