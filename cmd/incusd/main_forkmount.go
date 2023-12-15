package main

/*
#include "config.h"

#include <ctype.h>
#include <errno.h>
#include <fcntl.h>
#include <libgen.h>
#include <limits.h>
#include <lxc/lxccontainer.h>
#include <lxc/version.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "incus.h"
#include "memory_utils.h"
#include "mount_utils.h"
#include "syscall_numbers.h"
#include "syscall_wrappers.h"

#define VERSION_AT_LEAST(major, minor, micro)							\
	((LXC_DEVEL == 1) || (!(major > LXC_VERSION_MAJOR ||					\
	major == LXC_VERSION_MAJOR && minor > LXC_VERSION_MINOR ||				\
	major == LXC_VERSION_MAJOR && minor == LXC_VERSION_MINOR && micro > LXC_VERSION_MICRO)))

static int mkdir_p(const char *dir, mode_t mode)
{
	const char *tmp = dir;
	const char *orig = dir;

	do {
		__do_free char *makeme = NULL;

		dir = tmp + strspn(tmp, "/");
		tmp = dir + strcspn(dir, "/");
		makeme = strndup(orig, dir - orig);
		if (*makeme) {
			if (mkdir(makeme, mode) && errno != EEXIST) {
				fprintf(stderr, "failed to create directory '%s': %s\n", makeme, strerror(errno));
				return -1;
			}
		}
	} while(tmp != dir);

	return 0;
}

static void ensure_dir(char *dest) {
	struct stat sb;
	if (stat(dest, &sb) == 0) {
		if ((sb.st_mode & S_IFMT) == S_IFDIR)
			return;
		if (unlink(dest) < 0) {
			fprintf(stderr, "Failed to remove old %s: %s\n", dest, strerror(errno));
			_exit(1);
		}
	}
	if (mkdir(dest, 0755) < 0) {
		fprintf(stderr, "Failed to mkdir %s: %s\n", dest, strerror(errno));
		_exit(1);
	}
}

static void ensure_file(char *dest)
{
	__do_close int fd = -EBADF;
	struct stat sb;

	if (stat(dest, &sb) == 0) {
		if ((sb.st_mode & S_IFMT) != S_IFDIR)
			return;
		if (rmdir(dest) < 0) {
			fprintf(stderr, "Failed to remove old %s: %s\n", dest, strerror(errno));
			_exit(1);
		}
	}

	fd = creat(dest, 0755);
	if (fd < 0) {
		fprintf(stderr, "Failed to mkdir %s: %s\n", dest, strerror(errno));
		_exit(1);
	}
}

static void create(int fd_src, char *src, char *dest)
{
	__do_free char *dirdup = NULL;
	char *destdirname;
	struct stat sb;

	if (src) {
		if (stat(src, &sb) < 0)
			die("source %s does not exist", src);
	} else {
		if (fstat(fd_src, &sb) < 0)
			die("source %s does not exist", src);
	}

	dirdup = strdup(dest);
	if (!dirdup)
		_exit(1);

	destdirname = dirname(dirdup);

	if (mkdir_p(destdirname, 0755) < 0) {
		fprintf(stderr, "failed to create path: %s\n", destdirname);
		_exit(1);
	}

	switch (sb.st_mode & S_IFMT) {
	case S_IFDIR:
		ensure_dir(dest);
		return;
	default:
		ensure_file(dest);
		return;
	}
}

static int lxc_safe_ulong(const char *numstr, unsigned long *converted)
{
	char *err = NULL;
	unsigned long int uli;

	while (isspace(*numstr))
		numstr++;

	if (*numstr == '-')
		return -EINVAL;

	errno = 0;
	uli = strtoul(numstr, &err, 0);
	if (errno == ERANGE && uli == ULONG_MAX)
		return -ERANGE;

	if (err == numstr || *err != '\0')
		return -EINVAL;

	*converted = uli;
	return 0;
}

static void do_incus_forkmount(int pidfd, int ns_fd)
{
	unsigned long mntflags = 0;
	int fd_tree = -EBADF;
	int ret;
	char *src, *dest, *idmapType, *flags;

	src = advance_arg(true);
	dest = advance_arg(true);
	idmapType = advance_arg(true);
	flags = advance_arg(true);

	if (strcmp(idmapType, "idmapped") == 0) {
		int fd_mntns, fd_userns;

		fd_userns = preserve_ns(-ESRCH, ns_fd, "user");
		if (fd_userns < 0) {
			fprintf(stderr, "Failed to open user namespace of container: %s\n", strerror(errno));
			_exit(1);
		}

		fd_mntns = preserve_ns(getpid(), -EBADF, "mnt");
		if (fd_mntns < 0) {
			fprintf(stderr, "Failed to open mount namespace of container: %s\n", strerror(errno));
			_exit(1);
		}

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
			fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
			_exit(1);
		}

		fd_tree = mount_detach_idmap(src, fd_userns);
		if (fd_tree < 0) {
			fprintf(stderr, "Failed to create detached idmapped mount \"%s\": %s\n", src, strerror(errno));
			_exit(1);
		}

		ret = setns(fd_mntns, CLONE_NEWNS);
		if (ret) {
			fprintf(stderr, "Failed to switch to original mount namespace: %s\n", strerror(errno));
			_exit(1);
		}

		close_prot_errno_disarm(fd_userns);
		close_prot_errno_disarm(fd_mntns);
	}

	attach_userns_fd(ns_fd);

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
		fprintf(stderr, "Failed setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	create(-EBADF, src, dest);

	if (access(src, F_OK) < 0) {
		fprintf(stderr, "Mount source doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (access(dest, F_OK) < 0) {
		fprintf(stderr, "Mount destination doesn't exist: %s\n", strerror(errno));
		_exit(1);
	}

	if (fd_tree >= 0) {
		ret = incus_move_mount(fd_tree, "", -EBADF, dest, MOVE_MOUNT_F_EMPTY_PATH);
		if (ret) {
			fprintf(stderr, "Failed to move detached mount to target from %d to %s: %s\n", fd_tree, dest, strerror(errno));
			_exit(1);
		}

		close_prot_errno_disarm(fd_tree);
		_exit(0);
	}

	ret = lxc_safe_ulong(flags, &mntflags);
	if (ret < 0)
		_exit(1);

	// Here, we always move recursively, because we sometimes allow
	// recursive mounts. If the mount has no kids then it doesn't matter,
	// but if it does, we want to move those too.
	if (mount(src, dest, "none", MS_MOVE | MS_REC, NULL) < 0) {
		fprintf(stderr, "Failed mounting %s onto %s: %s\n", src, dest, strerror(errno));
		_exit(1);
	}

	_exit(0);
}

static int lxc_safe_uint(const char *numstr, unsigned int *converted)
{
	char *err = NULL;
	unsigned long int uli;

	while (isspace(*numstr))
		numstr++;

	if (*numstr == '-')
		return -EINVAL;

	errno = 0;
	uli = strtoul(numstr, &err, 0);
	if (errno == ERANGE && uli == ULONG_MAX)
		return -ERANGE;

	if (err == numstr || *err != '\0')
		return -EINVAL;

	if (uli > UINT_MAX)
		return -ERANGE;

	*converted = (unsigned int)uli;
	return 0;
}

static int mnt_attributes_new(unsigned int old_flags, unsigned int *new_flags)
{
	unsigned int flags = 0;

	if (old_flags & MS_RDONLY) {
		flags |= MOUNT_ATTR_RDONLY;
		old_flags &= ~MS_RDONLY;
	}

	if (old_flags & MS_NOSUID) {
		flags |= MOUNT_ATTR_NOSUID;
		old_flags &= ~MS_NOSUID;
	}

	if (old_flags & MS_NODEV) {
		flags |= MOUNT_ATTR_NODEV;
		old_flags &= ~MS_NODEV;
	}

	if (old_flags & MS_NOEXEC) {
		flags |= MOUNT_ATTR_NOEXEC;
		old_flags &= ~MS_NOEXEC;
	}

	if (old_flags & MS_RELATIME) {
		flags |= MOUNT_ATTR_RELATIME;
		old_flags &= ~MS_RELATIME;
	}

	if (old_flags & MS_NOATIME) {
		flags |= MOUNT_ATTR_NOATIME;
		old_flags &= ~MS_NOATIME;
	}

	if (old_flags & MS_STRICTATIME) {
		flags |= MOUNT_ATTR_STRICTATIME;
		old_flags &= ~MS_STRICTATIME;
	}

	if (old_flags & MS_NODIRATIME) {
		flags |= MOUNT_ATTR_NODIRATIME;
		old_flags &= ~MS_NODIRATIME;
	}

	*new_flags |= flags;
	return old_flags;
}

static int make_final_open(struct stat *st_src, const char *dest)
{
	int ret;
	int flags = O_CLOEXEC | O_NOFOLLOW | O_NOCTTY | O_CLOEXEC;
	struct stat st_dest;

	ret = stat(dest, &st_dest);
	if (ret == 0) {
		if ((st_dest.st_mode & S_IFMT) == (st_src->st_mode & S_IFMT))
			goto out_open;

		ret = remove(dest);
		if (ret)
			return -1;
	}

	if ((st_src->st_mode & S_IFMT) == S_IFDIR)
		ret = mkdir(dest, 0000);
	else
		ret = mknod(dest, S_IFREG | 0000, 0);
	if (ret)
		return -1;

out_open:
	return open(dest, flags | O_PATH);
}

static int make_dest_open(int fd_src, const char *dest)
{
	__do_free char *dirdup = NULL;
	int ret;
	char *destdirname;
	struct stat st_src;

	ret = fstat(fd_src, &st_src);
	if (ret)
		return -1;

	dirdup = strdup(dest);
	if (!dirdup)
		return -1;

	destdirname = dirname(dirdup);

	ret = mkdir_p(destdirname, 0755);
	if (ret)
		return -1;

	return make_final_open(&st_src, dest);
}

static void do_move_forkmount(int pidfd, int ns_fd)
{
	__do_close int fs_fd = -EBADF, mnt_fd = -EBADF, fd_userns = -EBADF,
		       dest_fd = -EBADF;
	int ret;
	char *fstype, *src, *dest, *idmapType, *flags;
	unsigned int old_mntflags = 0, new_mntflags = 0;

	fstype = advance_arg(true);
	src = advance_arg(true);
	dest = advance_arg(true);
	idmapType = advance_arg(true);
	flags = advance_arg(true);

	ret = lxc_safe_uint(flags, &old_mntflags);
	if (ret < 0)
		die("parse mount flags");

	mnt_attributes_new(old_mntflags, &new_mntflags);

	if (strcmp(fstype, "") && strcmp(fstype, "none")) {
		fs_fd = incus_fsopen(fstype, FSOPEN_CLOEXEC);
		if (fs_fd < 0)
			die("fsopen: %s", fstype);

		ret = incus_fsconfig(fs_fd, FSCONFIG_SET_STRING, "source", src, 0);
		if (ret < 0)
			die("fsconfig: source");

		ret = incus_fsconfig(fs_fd, FSCONFIG_CMD_CREATE, NULL, NULL, 0);
		if (ret < 0)
			die("fsconfig: create");

		mnt_fd = incus_fsmount(fs_fd, FSMOUNT_CLOEXEC, new_mntflags);
		if (mnt_fd < 0)
			die("fsmount");
	} else {
		mnt_fd = incus_open_tree(-EBADF, src, OPEN_TREE_CLOEXEC | OPEN_TREE_CLONE);
		if (mnt_fd < 0)
			die("open_tree");
	}

	fd_userns = preserve_ns(-ESRCH, ns_fd, "user");
	if (fd_userns < 0)
		die("preserve userns");

	if (strcmp(idmapType, "idmapped") == 0) {
		struct lxc_mount_attr attr = {
			.attr_set	= MOUNT_ATTR_IDMAP,
			.userns_fd	= fd_userns,

		};

		ret = incus_mount_setattr(mnt_fd, "", AT_EMPTY_PATH, &attr, sizeof(attr));
		if (ret)
			die("idmap mount");
	}

	attach_userns_fd(ns_fd);

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS))
		die("Failed setns to container mount namespace");

	dest_fd = make_dest_open(mnt_fd, dest);
	if (dest_fd < 0)
		die("Failed to create destination mount point");

	ret = incus_move_mount(mnt_fd, "", dest_fd, "",
			     MOVE_MOUNT_F_EMPTY_PATH | MOVE_MOUNT_T_EMPTY_PATH);
	if (ret)
		die("Failed to move detached mount to target from %d to %s", mnt_fd, dest);

	_exit(EXIT_SUCCESS);
}

static void do_incus_forkumount(int pidfd, int ns_fd)
{
	int ret;
	char *path = NULL;

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS)) {
		fprintf(stderr, "Failed to setns to container mount namespace: %s\n", strerror(errno));
		_exit(1);
	}

	path = advance_arg(true);

	ret = umount2(path, MNT_DETACH);
	if (ret < 0) {
		// - ENOENT: The user must have unmounted and removed the path.
		// - EINVAL: The user must have unmounted. Other explanations
		//           for EINVAL do not apply.
		if (errno == ENOENT || errno == EINVAL)
			_exit(0);

		fprintf(stderr, "Error unmounting %s: %s\n", path, strerror(errno));
		_exit(1);
	}

	_exit(0);
}

static void do_lxc_forkmount(void)
{
#if VERSION_AT_LEAST(3, 1, 0)
	int ret;
	char *config, *flags, *fstype, *lxcpath, *name, *source, *target;
	struct lxc_container *c;
	struct lxc_mount mnt = {0};
	unsigned long mntflags = 0;

	name = advance_arg(true);
	lxcpath = advance_arg(true);
	config = advance_arg(true);
	source = advance_arg(true);
	target = advance_arg(true);
	fstype = advance_arg(true);
	flags = advance_arg(true);

	c = lxc_container_new(name, lxcpath);
	if (!c)
		_exit(1);

	c->clear_config(c);

	if (!c->load_config(c, config)) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = lxc_safe_ulong(flags, &mntflags);
	if (ret < 0) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = c->mount(c, source, target, fstype, mntflags, NULL, &mnt);
	lxc_container_put(c);
	if (ret < 0)
		_exit(1);

	_exit(0);
#else
	fprintf(stderr, "error: Called lxc_forkmount when missing LXC support\n");
	_exit(1);
#endif
}

static void do_lxc_forkumount(void)
{
#if VERSION_AT_LEAST(3, 1, 0)
	int ret;
	char *config, *lxcpath, *name, *target;
	struct lxc_container *c;
	struct lxc_mount mnt = {0};

	name = advance_arg(true);
	lxcpath = advance_arg(true);
	config = advance_arg(true);
	target = advance_arg(true);

	c = lxc_container_new(name, lxcpath);
	if (!c)
		_exit(1);

	c->clear_config(c);

	if (!c->load_config(c, config)) {
		lxc_container_put(c);
		_exit(1);
	}

	ret = c->umount(c, target, MNT_DETACH, &mnt);
	lxc_container_put(c);
	if (ret < 0)
		_exit(1);

	_exit(0);
#else
	fprintf(stderr, "error: Called lxc_forkumount when missing LXC support\n");
	_exit(1);
#endif
}

void forkmount(void)
{
	char *command = NULL, *cur = NULL;
	int ns_fd = -EBADF, pidfd = -EBADF;
	pid_t pid = 0;

	// Get the subcommand
	command = advance_arg(false);
	if (command == NULL || (strcmp(command, "--help") == 0 || strcmp(command, "--version") == 0 || strcmp(command, "-h") == 0))
		return;

	// Check that we're root
	if (geteuid() != 0) {
		fprintf(stderr, "Error: forkmount requires root privileges\n");
		_exit(1);
	}

	// skip "--"
	advance_arg(true);

	// Call the subcommands
	if (strcmp(command, "go-mount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
			return;

		pid = atoi(cur);
		if (pid <= 0)
			_exit(EXIT_FAILURE);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(EXIT_FAILURE);

		do_incus_forkmount(pidfd, ns_fd);
	} else if (strcmp(command, "lxc-mount") == 0) {
		do_lxc_forkmount();
	} else if (strcmp(command, "move-mount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
			return;

		pid = atoi(cur);
		if (pid <= 0)
			_exit(EXIT_FAILURE);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(EXIT_FAILURE);

		do_move_forkmount(pidfd, ns_fd);
	} else if (strcmp(command, "go-umount") == 0) {
		// Get the pid
		cur = advance_arg(false);
		if (cur == NULL || (strcmp(cur, "--help") == 0 || strcmp(cur, "--version") == 0 || strcmp(cur, "-h") == 0))
			return;

		pid = atoi(cur);
		if (pid <= 0)
			_exit(EXIT_FAILURE);

		pidfd = atoi(advance_arg(true));
		ns_fd = pidfd_nsfd(pidfd, pid);
		if (ns_fd < 0)
			_exit(EXIT_FAILURE);

		do_incus_forkumount(pidfd, ns_fd);
	} else if (strcmp(command, "lxc-umount") == 0) {
		do_lxc_forkumount();
	}
}
*/
import "C"

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/incus/shared/cgo"
)

type cmdForkmount struct {
	global *cmdGlobal
}

func (c *cmdForkmount) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkmount"
	cmd.Short = "Perform mount operations"
	cmd.Long = `Description:
  Perform mount operations

  This set of internal commands are used for all container mount
  operations.
`
	cmd.Hidden = true

	// mount
	cmdLXCMount := &cobra.Command{}
	cmdLXCMount.Use = "lxc-mount <name> <lxcpath> <configpath> <source> <destination> <fstype> <flags>"
	cmdLXCMount.Args = cobra.ExactArgs(7)
	cmdLXCMount.RunE = c.Run
	cmd.AddCommand(cmdLXCMount)

	cmdGoMount := &cobra.Command{}
	cmdGoMount.Use = "go-mount <PID> <PidFd> <source> <destination> <idmapType> <flags>"
	cmdGoMount.Args = cobra.ExactArgs(6)
	cmdGoMount.RunE = c.Run
	cmd.AddCommand(cmdGoMount)

	// umount
	cmdLXCUmount := &cobra.Command{}
	cmdLXCUmount.Use = "lxc-umount <name> <lxcpath> <configpath> <path>"
	cmdLXCUmount.Args = cobra.ExactArgs(4)
	cmdLXCUmount.RunE = c.Run
	cmd.AddCommand(cmdLXCUmount)

	cmdGoUmount := &cobra.Command{}
	cmdGoUmount.Use = "go-umount <PID> <PidFd> <path>"
	cmdGoUmount.Args = cobra.ExactArgs(3)
	cmdGoUmount.RunE = c.Run
	cmd.AddCommand(cmdGoUmount)

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

func (c *cmdForkmount) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
