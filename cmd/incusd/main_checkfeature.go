package main

/*
#include "config.h"

#include <errno.h>
#include <fcntl.h>
#include <linux/types.h>
#include <poll.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sched.h>
#include <signal.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>
#include <syscall.h>
#include <linux/seccomp.h>
#include <linux/filter.h>
#include <linux/audit.h>
#include <sys/ptrace.h>
#include <sys/wait.h>

#include "incus.h"
#include "compiler.h"
#include "incus_seccomp.h"
#include "memory_utils.h"
#include "mount_utils.h"
#include "process_utils.h"
#include "syscall_numbers.h"
#include "syscall_wrappers.h"

#include "../../internal/netutils/netns_getifaddrs.c"

__ro_after_init bool core_scheduling_aware = false;
__ro_after_init bool close_range_aware = false;
__ro_after_init bool tiocgptpeer_aware = false;
__ro_after_init bool netnsid_aware = false;
__ro_after_init bool pidfd_aware = false;
__ro_after_init bool pidfd_thread_aware = false;
__ro_after_init bool pidfd_setns_aware = false;
__ro_after_init bool uevent_aware = false;
__ro_after_init bool binfmt_aware = false;
__ro_after_init int seccomp_notify_aware = 0;
__ro_after_init char errbuf[4096];

static int netns_set_nsid(int fd)
{
	__do_close int sockfd = -EBADF;
	int ret;
	char buf[NLMSG_ALIGN(sizeof(struct nlmsghdr)) +
		 NLMSG_ALIGN(sizeof(struct rtgenmsg)) +
		 NLMSG_ALIGN(1024)];
	struct nlmsghdr *hdr;
	struct rtgenmsg *msg;
	__s32 ns_id = -1;
	__u32 netns_fd = fd;

	sockfd = netlink_open(NETLINK_ROUTE);
	if (sockfd < 0)
		return -1;

	memset(buf, 0, sizeof(buf));
	hdr = (struct nlmsghdr *)buf;
	msg = (struct rtgenmsg *)NLMSG_DATA(hdr);

	hdr->nlmsg_len = NLMSG_LENGTH(sizeof(*msg));
	hdr->nlmsg_type = RTM_NEWNSID;
	hdr->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
	hdr->nlmsg_pid = 0;
	hdr->nlmsg_seq = RTM_NEWNSID;
	msg->rtgen_family = AF_UNSPEC;

	addattr(hdr, 1024, __LXC_NETNSA_FD, &netns_fd, sizeof(netns_fd));
	addattr(hdr, 1024, __LXC_NETNSA_NSID, &ns_id, sizeof(ns_id));

	ret = netlink_transaction(sockfd, hdr, hdr);
	if (ret < 0)
		return -1;

	return 0;
}

static void is_netnsid_aware(int *hostnetns_fd, int *newnetns_fd)
{
	__do_close int sock_fd = -EBADF;
	int netnsid, ret;

	*hostnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*hostnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve host network namespace");
		return;
	}

	ret = unshare(CLONE_NEWNET);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "Failed to unshare network namespace");
		return;
	}

	*newnetns_fd = open("/proc/self/ns/net", O_RDONLY | O_CLOEXEC);
	if (*newnetns_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to preserve new network namespace");
		return;
	}

	ret = netns_set_nsid(*hostnetns_fd);
	if (ret < 0) {
		(void)sprintf(errbuf, "%s", "failed to set network namespace identifier");
		return;
	}

	netnsid = netns_get_nsid(*hostnetns_fd);
	if (netnsid < 0) {
		(void)sprintf(errbuf, "%s", "Failed to get network namespace identifier");
		return;
	}

	sock_fd = socket(PF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
	if (sock_fd < 0) {
		(void)sprintf(errbuf, "%s", "Failed to open netlink routing socket");
		return;
	}

	ret = setsockopt(sock_fd, SOL_NETLINK, NETLINK_GET_STRICT_CHK, &(int){1}, sizeof(int));
	if (ret < 0) {
		// NETLINK_GET_STRICT_CHK isn't supported
		return;
	}

	// NETLINK_GET_STRICT_CHK is supported
	netnsid_aware = true;
}

static void is_uevent_aware(void)
{
	if (can_inject_uevent("placeholder", 6) < 0)
		return;

	uevent_aware = true;
}

static int user_trap_syscall(int nr, unsigned int flags)
{
	struct sock_filter filter[] = {
		BPF_STMT(BPF_LD+BPF_W+BPF_ABS,
			offsetof(struct seccomp_data, nr)),
		BPF_JUMP(BPF_JMP+BPF_JEQ+BPF_K, nr, 0, 1),
		BPF_STMT(BPF_RET+BPF_K, SECCOMP_RET_USER_NOTIF),
		BPF_STMT(BPF_RET+BPF_K, SECCOMP_RET_ALLOW),
	};

	struct sock_fprog prog = {
		.len = (unsigned short)ARRAY_SIZE(filter),
		.filter = filter,
	};

	return syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, flags, &prog);
}

// The ifdef can be safely ignored. We don't work on a kernel that old.
static int filecmp(pid_t pid1, pid_t pid2, int fd1, int fd2)
{
#ifdef __NR_kcmp
#ifndef KCMP_FILE
#define KCMP_FILE 0
#endif
	return syscall(__NR_kcmp, pid1, pid2, 0, fd1, fd2);
#else
	errno = ENOSYS;
	return -1;
#endif
}

__noreturn static void __do_user_notification_continue(void)
{
	__do_close int listener = -EBADF;
	pid_t pid;
	int ret;
	struct seccomp_notif req = {};
	struct seccomp_notif_resp resp = {};
	struct pollfd pollfd;

	listener = user_trap_syscall(__NR_dup, SECCOMP_FILTER_FLAG_NEW_LISTENER);
	if (listener < 0)
		_exit(EXIT_FAILURE);

	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid == 0) {
		int dup_fd, pipe_fds[2];
		pid_t self;

		// Don't bother cleaning up. On child exit all of those
		// will be closed anyway.
		ret = pipe(pipe_fds);
		if (ret < 0)
			_exit(EXIT_FAILURE);

		// O_CLOEXEC doesn't matter as we're in the child and we're
		// not going to exec.
		dup_fd = dup(pipe_fds[0]);
		if (dup_fd < 0)
			_exit(EXIT_FAILURE);

		self = getpid();

		ret = filecmp(self, self, pipe_fds[0], dup_fd);
		if (ret)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLIN))
		goto cleanup_sigkill;

	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_RECV, &req);
	if (ret)
		goto cleanup_sigkill;

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLOUT))
		goto cleanup_sigkill;

	if (req.data.nr != __NR_dup)
		goto cleanup_sigkill;

	resp.id = req.id;
	resp.flags |= SECCOMP_USER_NOTIF_FLAG_CONTINUE;
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
	resp.error = -EPERM;
	resp.flags = 0;
	if (ret) {
		ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
		goto cleanup_sigkill;
	}

cleanup_wait:
	ret = wait_for_pid(pid);
	if (ret)
		_exit(EXIT_FAILURE);
	_exit(EXIT_SUCCESS);

cleanup_sigkill:
	kill(pid, SIGKILL);
	goto cleanup_wait;
}

static void is_user_notification_continue_aware(void)
{
	int ret;
	pid_t pid;

	pid = fork();
	if (pid < 0)
		return;

	if (pid == 0) {
		__do_user_notification_continue();
		// Should not be reached.
		_exit(EXIT_FAILURE);
	}

	ret = wait_for_pid(pid);
	if (!ret)
		seccomp_notify_aware = 2;
}

__noreturn static void __do_user_notification_addfd(void)
{
	__do_close int listener = -EBADF;
	pid_t pid;
	int ret;
	struct seccomp_notif req = {};
	struct seccomp_notif_resp resp = {};
	struct seccomp_notif_addfd addfd = {};
	struct pollfd pollfd;

	listener = user_trap_syscall(__NR_dup, SECCOMP_FILTER_FLAG_NEW_LISTENER);
	if (listener < 0)
		_exit(EXIT_FAILURE);

	pid = fork();
	if (pid < 0)
		_exit(EXIT_FAILURE);

	if (pid == 0) {
		int dup_fd, pipe_fds[2];
		pid_t self;

		// Don't bother cleaning up. On child exit all of those
		// will be closed anyway.
		ret = pipe(pipe_fds);
		if (ret < 0)
			_exit(EXIT_FAILURE);

		// O_CLOEXEC doesn't matter as we're in the child and we're
		// not going to exec.
		dup_fd = dup(pipe_fds[0]);
		if (dup_fd < 0)
			_exit(EXIT_FAILURE);

		self = getpid();

		ret = filecmp(self, self, pipe_fds[0], dup_fd);
		if (ret)
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLIN))
		goto cleanup_sigkill;

	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_RECV, &req);
	if (ret)
		goto cleanup_sigkill;

	pollfd.fd = listener;
	pollfd.events = POLLIN | POLLOUT;

	ret = poll(&pollfd, 1, 5000);
	if (ret <= 0)
		goto cleanup_sigkill;

	if (!(pollfd.revents & POLLOUT))
		goto cleanup_sigkill;

	if (req.data.nr != __NR_dup)
		goto cleanup_sigkill;

	addfd.srcfd	= 3;
	addfd.id 	= req.id;
	addfd.flags 	= 0;

	// Inject the fd into the task.
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_ADDFD, &addfd);
	if (ret < 0)
		goto cleanup_sigkill;
	close(ret);

	resp.id = req.id;
	resp.flags |= SECCOMP_USER_NOTIF_FLAG_CONTINUE;
	ret = ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
	resp.error = -EPERM;
	resp.flags = 0;
	if (ret) {
		ioctl(listener, SECCOMP_IOCTL_NOTIF_SEND, &resp);
		goto cleanup_sigkill;
	}

cleanup_wait:
	ret = wait_for_pid(pid);
	if (ret)
		_exit(EXIT_FAILURE);
	_exit(EXIT_SUCCESS);

cleanup_sigkill:
	kill(pid, SIGKILL);
	goto cleanup_wait;
}

static void is_user_notification_addfd_aware(void)
{
	int ret;
	pid_t pid;

	pid = fork();
	if (pid < 0)
		return;

	if (pid == 0) {
		__do_user_notification_addfd();
		// Should not be reached.
		_exit(EXIT_FAILURE);
	}

	ret = wait_for_pid(pid);
	if (!ret)
		seccomp_notify_aware = 3;
}

static void is_seccomp_notify_aware(void)
{
	__u32 action[] = { SECCOMP_RET_USER_NOTIF };

	if (syscall(__NR_seccomp, SECCOMP_GET_ACTION_AVAIL, 0, &action[0]) == 0) {
		seccomp_notify_aware = 1;
		is_user_notification_continue_aware();
		if (seccomp_notify_aware == 2)
			is_user_notification_addfd_aware();
	}

}

static int is_pidfd_aware(void)
{
	__do_close int pidfd = -EBADF, pidfd_thread = -EBADF;
	int ret;

	pidfd = incus_pidfd_open(getpid(), 0);
	if (pidfd < 0)
		return -EBADF;

	// We don't care whether or not children were in a waitable state. We
	// just care whether waitid() recognizes P_PIDFD.
	ret = waitid(P_PIDFD, pidfd, NULL,
		    // Type of children to wait for.
		    __WALL |
		    // How to wait for them.
		    WNOHANG | WNOWAIT |
		    // What state to wait for.
		    WEXITED | WSTOPPED | WCONTINUED);
	if (ret < 0 && errno != ECHILD)
		return -errno;

	ret = incus_pidfd_send_signal(pidfd, 0, NULL, 0);
	if (ret)
		return -errno;

	pidfd_thread = incus_pidfd_open(getpid(), PIDFD_THREAD);
	if (pidfd_thread >= 0)
		pidfd_thread_aware = true;

	pidfd_aware = true;
	return move_fd(pidfd);
}

#ifndef TIOCGPTPEER
	#if defined __sparc__
		#define TIOCGPTPEER _IO('t', 137)
	#else
		#define TIOCGPTPEER _IO('T', 0x41)
	#endif
#endif

static void is_tiocgptpeer_aware(void)
{
	__do_close int ptx_fd = -EBADF, pty_fd = -EBADF;
	int ret;

	ptx_fd = open("/dev/ptmx", O_RDWR | O_NOCTTY | O_CLOEXEC);
	if (ptx_fd < 0)
		return;

	ret = grantpt(ptx_fd);
	if (ret < 0)
		return;

	ret = unlockpt(ptx_fd);
	if (ret < 0)
		return;

	pty_fd = ioctl(ptx_fd, TIOCGPTPEER, O_RDWR | O_NOCTTY | O_CLOEXEC);
	if (pty_fd < 0)
		return;

	tiocgptpeer_aware = true;
}

static void is_close_range_aware(void)
{
	int fd;

	fd = open("/dev/null", O_RDONLY | O_CLOEXEC);
	if (fd < 0)
		return;

	if (incus_close_range(fd, fd, CLOSE_RANGE_UNSHARE))
		return;

	close_range_aware = true;
}

static void is_core_scheduling_aware(void)
{
	int ret;
	pid_t pid;

	pid = fork();
	if (pid < 0)
		return;

	if (pid == 0) {
		pid_t pid_self;
		__u64 core_sched_cookie;

		pid_self = getpid();

		ret = core_scheduling_cookie_create_threadgroup(pid_self);
		if (ret)
			_exit(EXIT_FAILURE);

		core_sched_cookie = core_scheduling_cookie_get(pid_self);
		if (!core_scheduling_cookie_valid(core_sched_cookie))
			_exit(EXIT_FAILURE);

		_exit(EXIT_SUCCESS);
	}

	ret = wait_for_pid(pid);
	if (ret)
		return;

	core_scheduling_aware = true;
}

static void is_binfmt_aware(void)
{
	int ret;
	pid_t pid;
	int fd = EBADF;

	pid = fork();
	if (pid < 0) {
		(void)sprintf(errbuf, "%s", "Failed to spawn subprocess");
		return;
	}

	if (pid == 0) {
		// Create namespaces
		ret = unshare(CLONE_NEWNS|CLONE_NEWUSER);
		if (ret < 0) {
			(void)sprintf(errbuf, "%s", "Failed to unshare network and user namespace");
			_exit(EXIT_FAILURE);
		}

		// Setup uid_map
		fd = openat(AT_FDCWD, "/proc/self/uid_map", O_WRONLY);
		if (ret < 0) {
			(void)sprintf(errbuf, "%s", "Failed to open uid_map");
			_exit(EXIT_FAILURE);
		}

		if (write(fd, "0 0 1", 5) != 5) {
			(void)sprintf(errbuf, "%s", "Failed to write uid_map");
			_exit(EXIT_FAILURE);
		}

		if (close(fd) < 0) {
			(void)sprintf(errbuf, "%s", "Failed to close uid_map");
			_exit(EXIT_FAILURE);
		}

		// Setup setgroups
		fd = openat(AT_FDCWD, "/proc/self/setgroups", O_WRONLY);
		if (ret < 0) {
			(void)sprintf(errbuf, "%s", "Failed to open setgroups");
			_exit(EXIT_FAILURE);
		}

		if (write(fd, "deny", 4) != 4) {
			(void)sprintf(errbuf, "%s", "Failed to write setgroups");
			_exit(EXIT_FAILURE);
		}

		if (close(fd) < 0) {
			(void)sprintf(errbuf, "%s", "Failed to close setgroups");
			_exit(EXIT_FAILURE);
		}

		// Setup gid_map
		fd = openat(AT_FDCWD, "/proc/self/gid_map", O_WRONLY);
		if (ret < 0) {
			(void)sprintf(errbuf, "%s", "Failed to open gid_map");
			_exit(EXIT_FAILURE);
		}

		if (write(fd, "0 0 1", 5) != 5) {
			(void)sprintf(errbuf, "%s", "Failed to write gid_map");
			_exit(EXIT_FAILURE);
		}

		if (close(fd) < 0) {
			(void)sprintf(errbuf, "%s", "Failed to close gid_map");
			_exit(EXIT_FAILURE);
		}

		// Re-mount / private
		ret = mount("none", "/", NULL, MS_REC|MS_PRIVATE, NULL);
		if (ret < 0) {
			(void)sprintf(errbuf, "%s", "Failed to mount remount /");
			_exit(EXIT_FAILURE);
		}

		// Attempt mounting binfmt_misc
		ret = mount("binfmt", "/", "binfmt_misc", 0, NULL);
		if (ret < 0) {
			_exit(EXIT_FAILURE);
		}

		_exit(EXIT_SUCCESS);
	}

	ret = wait_for_pid(pid);
	if (ret)
		return;

	binfmt_aware = true;
}

void checkfeature(void)
{
	__do_close int hostnetns_fd = -EBADF, newnetns_fd = -EBADF, pidfd = -EBADF;

	is_netnsid_aware(&hostnetns_fd, &newnetns_fd);
	pidfd = is_pidfd_aware();
	is_uevent_aware();
	is_seccomp_notify_aware();
	is_tiocgptpeer_aware();
	is_close_range_aware();
	is_core_scheduling_aware();

	if (pidfd >= 0)
		pidfd_setns_aware = !setns(pidfd, CLONE_NEWNET);

	if (setns(hostnetns_fd, CLONE_NEWNET) < 0)
		(void)sprintf(errbuf, "%s", "Failed to attach to host network namespace");

	is_binfmt_aware();
}

static bool is_empty_string(char *s)
{
	return (errbuf[0] == '\0');
}

static bool kernel_supports_idmapped_mounts(void)
{
	__do_close int fd_devnull = -EBADF, fd_tree = -EBADF;
	struct lxc_mount_attr attr = {
	    .attr_set		= MOUNT_ATTR_IDMAP,

	};
	int ret;

	fd_tree = incus_open_tree(-EBADF, "/", OPEN_TREE_CLONE | OPEN_TREE_CLOEXEC);
	if (fd_tree < 0)
		return false;

	fd_devnull = open("/dev/null", O_PATH | O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NOCTTY);
	if (fd_devnull < 0)
		return false;

	// If the kernel supports idmapped mounts at all we will get a EBADF
	// for trying to create one from an invalid O_PATH fd.
	attr.userns_fd = fd_devnull;
	ret = incus_mount_setattr(fd_tree, "", AT_EMPTY_PATH, &attr, sizeof(attr));
	if (ret && (errno == EBADF))
		return true;

	return false;
}
*/
import "C"

import (
	_ "github.com/lxc/incus/shared/cgo" // Used by cgo
	"github.com/lxc/incus/shared/logger"
)

func canUseNetnsGetifaddrs() bool {
	if !bool(C.is_empty_string(&C.errbuf[0])) {
		logger.Debugf("%s", C.GoString(&C.errbuf[0]))
	}

	return bool(C.netnsid_aware)
}

func canUseUeventInjection() bool {
	return bool(C.uevent_aware)
}

func canUseSeccompListener() bool {
	return bool(C.seccomp_notify_aware > 0)
}

func canUseSeccompListenerContinue() bool {
	return bool(C.seccomp_notify_aware >= 2)
}

func canUseSeccompListenerAddfd() bool {
	return bool(C.seccomp_notify_aware == 3)
}

func canUsePidFds() bool {
	return bool(C.pidfd_aware)
}

func canUseThreadPidFds() bool {
	return bool(C.pidfd_thread_aware)
}

// We're only using this during daemon startup to give an indication whether
// the underlying kernel has the necessary infrastructure to support idmapped
// mounts. This check does not give any indication whether the relevant
// filesystem used for a container does have this support.
func kernelSupportsIdmappedMounts() bool {
	return bool(C.kernel_supports_idmapped_mounts())
}

func canUseNativeTerminals() bool {
	return bool(C.tiocgptpeer_aware)
}

func canUseCloseRange() bool {
	return bool(C.close_range_aware)
}

func canUsePidFdSetns() bool {
	return bool(C.pidfd_setns_aware)
}

func canUseCoreScheduling() bool {
	return bool(C.core_scheduling_aware)
}

func canUseBinfmt() bool {
	return bool(C.binfmt_aware)
}
