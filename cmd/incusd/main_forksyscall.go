package main

/*
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <fcntl.h>
#include <libgen.h>
#include <sched.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/capability.h>
#include <sys/fsuid.h>
#include <sys/prctl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/vfs.h>
#include <sys/wait.h>
#include <sys/xattr.h>
#include <unistd.h>

#include "../../shared/cgo/macro.h"
#include "../../shared/cgo/memory_utils.h"
#include "../../shared/cgo/mount_utils.h"
#include "../../shared/cgo/syscall_numbers.h"
#include "../../shared/cgo/syscall_wrappers.h"

extern char* advance_arg(bool required);
extern void attach_userns_fd(int ns_fd);
extern int pidfd_nsfd(int pidfd, pid_t pid);
extern int preserve_ns(pid_t pid, int ns_fd, const char *ns);
extern bool change_namespaces(int pidfd, int nsfd, unsigned int flags);
extern int mount_detach_idmap(const char *path, int fd_userns);

static bool chdirchroot_in_mntns(int cwd_fd, int root_fd)
{
	ssize_t len;
	char buf[PATH_MAX];

	if (fchdir(cwd_fd))
		return false;

	len = readlinkat(root_fd, "", buf, sizeof(buf));
	if (len < 0 || len >= sizeof(buf))
		return false;
	buf[len] = '\0';

	if (chroot(buf))
		return false;

	return true;
}

static bool acquire_basic_creds(pid_t pid, int pidfd, int ns_fd,
				int *rootfd, int *cwdfd)
{
	__do_close int cwd_fd = -EBADF, root_fd = -EBADF;
	char buf[256];

	snprintf(buf, sizeof(buf), "/proc/%d/root", pid);
	root_fd = open(buf, O_PATH | O_RDONLY | O_CLOEXEC | O_NOFOLLOW);
	if (root_fd < 0)
		return false;

	snprintf(buf, sizeof(buf), "/proc/%d/cwd", pid);
	cwd_fd = open(buf, O_PATH | O_RDONLY | O_CLOEXEC);
	if (cwd_fd < 0)
		return false;

	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS))
		return false;

	if (!chdirchroot_in_mntns(cwd_fd, root_fd))
		return false;

	if (rootfd)
		*rootfd = move_fd(root_fd);

	if (cwdfd)
		*cwdfd = move_fd(cwd_fd);
	return true;
}

static bool reacquire_basic_creds(int pidfd, int ns_fd,
				  int root_fd, int cwd_fd)
{
	if (!change_namespaces(pidfd, ns_fd, CLONE_NEWNS))
		return false;

	if (!chdirchroot_in_mntns(cwd_fd, root_fd))
		return false;

	return true;
}

static bool acquire_final_creds(pid_t pid, uid_t uid, gid_t gid, uid_t fsuid, gid_t fsgid)
{
	int ret;
	cap_t caps;

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = prctl(PR_SET_KEEPCAPS, 1);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = setegid(gid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	setfsgid(fsgid);
	if (setfsgid(-1) != fsgid) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}


	ret = seteuid(uid);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	setfsuid(fsuid);
	if (setfsuid(-1) != fsuid) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	ret = cap_set_proc(caps);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		return false;
	}

	return true;
}

// Expects command line to be in the form:
// <PID> <root-uid> <root-gid> <path> <mode> <dev>
static void mknod_emulate(void)
{
	__do_close int target_dir_fd = -EBADF, pidfd = -EBADF, ns_fd = -EBADF;
	char *target = NULL, *target_dir = NULL;
	int ret;
	char path[PATH_MAX];
	mode_t mode;
	dev_t dev;
	pid_t pid;
	uid_t fsuid, uid;
	gid_t fsgid, gid;
	struct statfs sfs;

	pid = atoi(advance_arg(true));
	pidfd = atoi(advance_arg(true));
	ns_fd = pidfd_nsfd(pidfd, pid);
	if (ns_fd < 0)
		_exit(EXIT_FAILURE);
	target = advance_arg(true);
	mode = atoi(advance_arg(true));
	dev = atoi(advance_arg(true));
	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	fsuid = atoi(advance_arg(true));
	fsgid = atoi(advance_arg(true));

	if (!acquire_basic_creds(pid, pidfd, ns_fd, NULL, NULL)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	snprintf(path, sizeof(path), "%s", target);
	target_dir = dirname(path);
	target_dir_fd = open(target_dir, O_PATH | O_RDONLY | O_CLOEXEC | O_DIRECTORY);
	if (target_dir_fd < 0) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (!acquire_final_creds(pid, uid, gid, fsuid, fsgid)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	ret = fstatfs(target_dir_fd, &sfs);
	if (ret) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (sfs.f_flags & MS_NODEV) {
		fprintf(stderr, "%d", EPERM);
		_exit(EXIT_FAILURE);
	}

	// basename() can modify its argument so accessing target_host is
	// invalid from now on.
	ret = mknodat(target_dir_fd, target, mode, dev);
	if (ret) {
		if (errno == EPERM)
			fprintf(stderr, "%d", ENOMEDIUM);
		else
			fprintf(stderr, "%d", errno);

		_exit(EXIT_FAILURE);
	}
}

const static int ns_flags[] = { CLONE_NEWUSER, CLONE_NEWPID, CLONE_NEWUTS, CLONE_NEWIPC, CLONE_NEWNET, CLONE_NEWCGROUP };

static bool change_creds(int pidfd, int ns_fd, cap_t caps, uid_t nsuid, gid_t nsgid, uid_t nsfsuid, gid_t nsfsgid)
{
	if (prctl(PR_SET_KEEPCAPS, 1))
		return false;

	for (int i = 0; ARRAY_SIZE(ns_flags); i++) {
		if (!change_namespaces(pidfd, ns_fd, ns_flags[i]))
			return false;
	}

	if (setegid(nsgid))
		return false;

	setfsgid(nsfsgid);

	if (seteuid(nsuid))
		return false;

	setfsuid(nsfsuid);

	if (cap_set_proc(caps))
		return false;

	return true;
}

static void setxattr_emulate(void)
{
	__do_close int ns_fd = -EBADF, pidfd = -EBADF, target_fd = -EBADF;
	int flags = 0;
	char *name, *target;
	uid_t nsfsuid, nsuid;
	gid_t nsfsgid, nsgid;
	pid_t pid = 0;
	cap_t caps;
	cap_flag_value_t flag;
	int whiteout;
	void *data;
	size_t size;

	pid = atoi(advance_arg(true));
	pidfd = atoi(advance_arg(true));
	ns_fd = pidfd_nsfd(pidfd, pid);
	if (ns_fd < 0)
		_exit(EXIT_FAILURE);
	nsuid = atoi(advance_arg(true));
	nsgid = atoi(advance_arg(true));
	nsfsuid = atoi(advance_arg(true));
	nsfsgid = atoi(advance_arg(true));
	name = advance_arg(true);
	target = advance_arg(true);
	flags = atoi(advance_arg(true));
	whiteout = atoi(advance_arg(true));
	size = atoi(advance_arg(true));
	data = advance_arg(true);

	if (!acquire_basic_creds(pid, pidfd, ns_fd, NULL, NULL)) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	target_fd = open(target, O_RDONLY | O_CLOEXEC);
	if (target_fd < 0) {
		fprintf(stderr, "%d", errno);
		_exit(EXIT_FAILURE);
	}

	caps = cap_get_pid(pid);
	if (!caps) {
		fprintf(stderr, "%d", ENOANO);
		_exit(EXIT_FAILURE);
	}

	if (whiteout == 1) {
		if (cap_get_flag(caps,  CAP_SYS_ADMIN, CAP_EFFECTIVE, &flag) != 0) {
			fprintf(stderr, "%d", EPERM);
			_exit(EXIT_FAILURE);
		}

		if (flag == CAP_CLEAR) {
			fprintf(stderr, "%d", EPERM);
			_exit(EXIT_FAILURE);
		}
	}

	if (whiteout == 1) {
		if (fsetxattr(target_fd, "trusted.overlay.opaque", "y", 1, flags)) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	} else {
		if (!change_creds(pidfd, ns_fd, caps, nsuid, nsgid, nsfsuid, nsfsgid)) {
			fprintf(stderr, "%d", EFAULT);
			_exit(EXIT_FAILURE);
		}

		if (fsetxattr(target_fd, name, data, size, flags)) {
			fprintf(stderr, "%d", errno);
			_exit(EXIT_FAILURE);
		}
	}
}

static void mount_emulate(void)
{
	__do_close int fd_mntns = -EBADF, fd_userns = -EBADF, pidfd = -EBADF,
		       ns_fd = -EBADF, root_fd = -EBADF, cwd_fd = -EBADF;
	char *source = NULL, *shift = NULL, *target = NULL, *fstype = NULL;
	bool use_fuse;
	uid_t nsuid = -1, uid = -1, nsfsuid = -1, fsuid = -1;
	gid_t nsgid = -1, gid = -1, nsfsgid = -1, fsgid = -1;
	int ret;
	pid_t pid = -1;
	unsigned long flags = 0;
	const void *data;

	pid = atoi(advance_arg(true));
	pidfd = atoi(advance_arg(true));
	ns_fd = pidfd_nsfd(pidfd, pid);
	if (ns_fd < 0)
		_exit(EXIT_FAILURE);
	use_fuse = (atoi(advance_arg(true)) == 1);
	if (!use_fuse) {
		source = advance_arg(true);
		target = advance_arg(true);
		fstype = advance_arg(true);
		flags = atoi(advance_arg(true));
		shift = advance_arg(true);
	}

	uid = atoi(advance_arg(true));
	gid = atoi(advance_arg(true));
	fsuid = atoi(advance_arg(true));
	fsgid = atoi(advance_arg(true));
	if (!use_fuse) {
		nsuid = atoi(advance_arg(true));
		nsgid = atoi(advance_arg(true));
		nsfsuid = atoi(advance_arg(true));
		nsfsgid = atoi(advance_arg(true));
		data = advance_arg(false);
	}

	fd_userns = preserve_ns(-ESRCH, ns_fd, "user");
	if (fd_userns < 0)
		_exit(EXIT_FAILURE);

	fd_mntns = preserve_ns(getpid(), ns_fd, "mnt");
	if (fd_mntns < 0)
		_exit(EXIT_FAILURE);

	if (use_fuse) {
		attach_userns_fd(ns_fd);

		// Attach to pid namespace so that if we spawn a fuse daemon
		// it'll belong to the correct pid namespace and dies with the
		// container.
		change_namespaces(pidfd, ns_fd, CLONE_NEWPID);
	}

	if (!acquire_basic_creds(pid, pidfd, ns_fd, &root_fd, &cwd_fd))
		_exit(EXIT_FAILURE);

	if (!acquire_final_creds(pid, uid, gid, fsuid, fsgid))
		_exit(EXIT_FAILURE);

	if (use_fuse) {
		int status;
		pid_t pid_fuse;

		pid_fuse = fork();
		if (pid_fuse < 0)
			_exit(EXIT_FAILURE);

		if (pid_fuse == 0) {
			const char *fuse_source, *fuse_target, *fuse_opts;

			fuse_source = advance_arg(true);
			fuse_target = advance_arg(true);
			fuse_opts = advance_arg(true);

			if (strcmp(fuse_opts, "") == 0)
				execlp("mount.fuse", "mount.fuse", fuse_source, fuse_target, (char *) NULL);
			else
				execlp("mount.fuse", "mount.fuse", fuse_source, fuse_target, "-o", fuse_opts, (char *) NULL);
			_exit(EXIT_FAILURE);
		}

		ret = waitpid(pid_fuse, &status, 0);
		if ((ret != pid_fuse) || !WIFEXITED(status) || WEXITSTATUS(status))
			_exit(EXIT_FAILURE);
	} else if (strcmp(shift, "idmapped") == 0) {
		int fd_tree;
		int fs_fd = -EBADF;

		struct lxc_mount_attr attr = {
		    .attr_set		= MOUNT_ATTR_IDMAP,
		};

		fs_fd = incus_fsopen(fstype, FSOPEN_CLOEXEC);
		if (fs_fd < 0)
			die("error: failed to create detached idmapped mount: fsopen");

		ret = incus_fsconfig(fs_fd, FSCONFIG_SET_STRING, "source", source, 0);
		if (ret < 0)
			die("error: failed to create detached idmapped mount: fsconfig");

		ret = incus_fsconfig(fs_fd, FSCONFIG_CMD_CREATE, NULL, NULL, 0);
		if (ret < 0)
			die("error: failed to create detached idmapped mount: fsconfig");

		fd_tree = incus_fsmount(fs_fd, FSMOUNT_CLOEXEC, flags);
		if (fd_tree < 0)
			die("error: failed to create detached idmapped mount: fsmount");

		attr.userns_fd = fd_userns;
		ret = incus_mount_setattr(fd_tree, "", AT_EMPTY_PATH, &attr, sizeof(attr));
		if (ret < 0)
			die("error: failed to create detached idmapped mount");

		ret = setns(fd_mntns, CLONE_NEWNS);
		if (ret)
			die("error: failed to attach to old mount namespace");

		attach_userns_fd(ns_fd);

		if (!change_namespaces(pidfd, ns_fd, CLONE_NEWUSER))
			die("error: failed to change to target user namespace");

		if (!reacquire_basic_creds(pidfd, ns_fd, root_fd, cwd_fd))
			die("error: failed to acquire basic creds");

		if (!acquire_final_creds(pid, nsuid, nsgid, nsfsuid, nsfsgid))
			die("error: failed to acquire final creds");

		ret = incus_move_mount(fd_tree, "", -EBADF, target, MOVE_MOUNT_F_EMPTY_PATH);
		if (ret)
			die("error: failed to attach detached mount");
	} else {
		if (mount(source, target, fstype, flags, data) < 0)
			_exit(EXIT_FAILURE);
	}
}

static bool incus_cap_is_set(cap_t caps, cap_value_t cap, cap_flag_t flag)
{
	int ret;
	cap_flag_value_t flagval;

	ret = cap_get_flag(caps, cap, flag, &flagval);
	if (ret < 0)
		return false;

	return flagval == CAP_SET;
}

static void sched_setscheduler_emulate(void)
{
	__do_close int pidfd = -EBADF, ns_fd = -EBADF;
	pid_t pid_caller = -ESRCH, pid_target = -ESRCH;
	int policy = -1, sched_priority = -1;
	int switch_pidns = 0;
	struct sched_param param = {};
	cap_t caps;

	pid_caller = atoi(advance_arg(true));

	pidfd = atoi(advance_arg(true));
	ns_fd = pidfd_nsfd(pidfd, pid_caller);
	if (ns_fd < 0)
		_exit(EXIT_FAILURE);

	switch_pidns = atoi(advance_arg(true));

	pid_target = atoi(advance_arg(true));
	if (pid_target < 0)
		_exit(EXIT_FAILURE);

	policy = atoi(advance_arg(true));
	if (policy < 0)
		_exit(EXIT_FAILURE);

	sched_priority = atoi(advance_arg(true));
	if (sched_priority < 0)
		_exit(EXIT_FAILURE);

	param.sched_priority = sched_priority;

	caps = cap_get_pid(pid_caller);
	if (!caps)
		_exit(EXIT_FAILURE);

	if (!incus_cap_is_set(caps, CAP_SYS_NICE, CAP_EFFECTIVE))
		_exit(EXIT_FAILURE);

	if (switch_pidns && !change_namespaces(pidfd, ns_fd, CLONE_NEWPID))
		_exit(EXIT_FAILURE);

	if (sched_setscheduler(pid_target, policy, &param))
		_exit(EXIT_FAILURE);
}

void forksyscall(void)
{
	char *syscall = NULL;

	// Check that we're root
	if (geteuid() != 0)
		_exit(EXIT_FAILURE);

	// Get the subcommand
	syscall = advance_arg(false);
	if (syscall == NULL ||
	    (strcmp(syscall, "--help") == 0 ||
	     strcmp(syscall, "--version") == 0 || strcmp(syscall, "-h") == 0))
		_exit(EXIT_SUCCESS);

	if (strcmp(syscall, "mknod") == 0)
		mknod_emulate();
	else if (strcmp(syscall, "sched_setscheduler") == 0)
		sched_setscheduler_emulate();
	else if (strcmp(syscall, "setxattr") == 0)
		setxattr_emulate();
	else if (strcmp(syscall, "mount") == 0)
		mount_emulate();
	else
		_exit(EXIT_FAILURE);

	_exit(EXIT_SUCCESS);
}
*/
import "C"

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/incus/v6/shared/cgo"
)

type cmdForksyscall struct {
	global *cmdGlobal
}

func (c *cmdForksyscall) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forksyscall <syscall> <PID> <PidFd> [...]"
	cmd.Short = "Perform syscall operations"
	cmd.Long = `Description:
  Perform syscall operations

  This set of internal commands is used for all seccomp-based container syscall
  operations.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForksyscall) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
