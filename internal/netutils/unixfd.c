
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <errno.h>
#include <fcntl.h>
#include <grp.h>
#include <limits.h>
#include <poll.h>
#include <pty.h>
#include <pwd.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/stat.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/un.h>

#include "unixfd.h"
#include "../../shared/cgo/memory_utils.h"

int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
			       void *data, size_t size)
{
	__do_free char *cmsgbuf = NULL;
	struct msghdr msg;
	struct iovec iov;
	struct cmsghdr *cmsg = NULL;
	char buf[1] = {0};
	size_t cmsgbufsize = CMSG_SPACE(num_sendfds * sizeof(int));

	memset(&msg, 0, sizeof(msg));
	memset(&iov, 0, sizeof(iov));

	cmsgbuf = malloc(cmsgbufsize);
	if (!cmsgbuf)
		return -1;

	msg.msg_control = cmsgbuf;
	msg.msg_controllen = cmsgbufsize;

	cmsg = CMSG_FIRSTHDR(&msg);
	cmsg->cmsg_level = SOL_SOCKET;
	cmsg->cmsg_type = SCM_RIGHTS;
	cmsg->cmsg_len = CMSG_LEN(num_sendfds * sizeof(int));

	msg.msg_controllen = cmsg->cmsg_len;

	memcpy(CMSG_DATA(cmsg), sendfds, num_sendfds * sizeof(int));

	iov.iov_base = data ? data : buf;
	iov.iov_len = data ? size : sizeof(buf);
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;

	return sendmsg(fd, &msg, MSG_NOSIGNAL);
}

ssize_t lxc_abstract_unix_recv_fds_iov(int fd, struct unix_fds *ret_fds,
				       struct iovec *ret_iov, size_t size_ret_iov)
{
	__do_free char *cmsgbuf = NULL;
	ssize_t ret;
	struct msghdr msg = {};
	struct cmsghdr *cmsg = NULL;
	size_t cmsgbufsize = CMSG_SPACE(sizeof(struct ucred)) +
			     CMSG_SPACE(ret_fds->fd_count_max * sizeof(int));

	if (ret_fds->flags & ~UNIX_FDS_ACCEPT_MASK)
		return ret_errno(EINVAL);

	if (hweight32((ret_fds->flags & ~UNIX_FDS_ACCEPT_NONE)) > 1)
		return ret_errno(EINVAL);

	if (ret_fds->fd_count_max >= KERNEL_SCM_MAX_FD)
		return ret_errno(EINVAL);

	if (ret_fds->fd_count_ret != 0)
		return ret_errno(EINVAL);

	cmsgbuf = zalloc(cmsgbufsize);
	if (!cmsgbuf)
		return ret_errno(ENOMEM);

	msg.msg_control		= cmsgbuf;
	msg.msg_controllen	= cmsgbufsize;

	msg.msg_iov	= ret_iov;
	msg.msg_iovlen	= size_ret_iov;

again:
	ret = recvmsg(fd, &msg, MSG_CMSG_CLOEXEC);
	if (ret < 0) {
		if (errno == EINTR)
			goto again;

		return -errno;
	}
	if (ret == 0)
		return 0;

	/* If SO_PASSCRED is set we will always get a ucred message. */
	for (cmsg = CMSG_FIRSTHDR(&msg); cmsg; cmsg = CMSG_NXTHDR(&msg, cmsg)) {
                if (cmsg->cmsg_level == SOL_SOCKET && cmsg->cmsg_type == SCM_RIGHTS) {
			__u32 idx;
			/*
			 * This causes some compilers to complain about
			 * increased alignment requirements but I haven't found
			 * a better way to deal with this yet. Suggestions
			 * welcome!
			 */
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wcast-align"
			int *fds_raw = (int *)CMSG_DATA(cmsg);
#pragma GCC diagnostic pop
			__u32 num_raw = (cmsg->cmsg_len - CMSG_LEN(0)) / sizeof(int);

			/*
			 * We received an insane amount of file descriptors
			 * which exceeds the kernel limit we know about so
			 * close them and return an error.
			 */
			if (num_raw >= KERNEL_SCM_MAX_FD) {
				for (idx = 0; idx < num_raw; idx++)
					close(fds_raw[idx]);

				return -EFBIG;
			}

			if (msg.msg_flags & MSG_CTRUNC) {
				for (idx = 0; idx < num_raw; idx++)
					close(fds_raw[idx]);

				return -EFBIG;
			}

			if (ret_fds->fd_count_max > num_raw) {
				if (!(ret_fds->flags & UNIX_FDS_ACCEPT_LESS)) {
					for (idx = 0; idx < num_raw; idx++)
						close(fds_raw[idx]);

					return -EINVAL;
				}

				/*
				 * Make sure any excess entries in the fd array
				 * are set to -EBADF so our cleanup functions
				 * can safely be called.
				 */
				for (idx = num_raw; idx < ret_fds->fd_count_max; idx++)
					ret_fds->fd[idx] = -EBADF;

				ret_fds->flags |= UNIX_FDS_RECEIVED_LESS;
			} else if (ret_fds->fd_count_max < num_raw) {
				if (!(ret_fds->flags & UNIX_FDS_ACCEPT_MORE)) {
					for (idx = 0; idx < num_raw; idx++)
						close(fds_raw[idx]);

					return -EINVAL;
				}

				/* Make sure we close any excess fds we received. */
				for (idx = ret_fds->fd_count_max; idx < num_raw; idx++)
					close(fds_raw[idx]);

				/* Cap the number of received file descriptors. */
				num_raw = ret_fds->fd_count_max;
				ret_fds->flags |= UNIX_FDS_RECEIVED_MORE;
			} else {
				ret_fds->flags |= UNIX_FDS_RECEIVED_EXACT;
			}

			if (hweight32((ret_fds->flags & ~UNIX_FDS_ACCEPT_MASK)) > 1) {
				for (idx = 0; idx < num_raw; idx++)
					close(fds_raw[idx]);

				return -EINVAL;
			}

			memcpy(ret_fds->fd, CMSG_DATA(cmsg), num_raw * sizeof(int));
			ret_fds->fd_count_ret = num_raw;
			break;
		}
	}

	if (ret_fds->fd_count_ret == 0) {
		ret_fds->flags |= UNIX_FDS_RECEIVED_NONE;

		/* We expected to receive file descriptors. */
		if ((ret_fds->flags & UNIX_FDS_ACCEPT_MASK) &&
		    !(ret_fds->flags & UNIX_FDS_ACCEPT_NONE))
			return -EINVAL;
	}

	return ret;
}

ssize_t lxc_abstract_unix_recv_fds(int fd, struct unix_fds *ret_fds,
				   void *ret_data, size_t size_ret_data)
{
	char buf[1] = {};
	struct iovec iov = {
		.iov_base	= ret_data ? ret_data : buf,
		.iov_len	= ret_data ? size_ret_data : sizeof(buf),
	};
	ssize_t ret;

	ret = lxc_abstract_unix_recv_fds_iov(fd, ret_fds, &iov, 1);
	if (ret < 0)
		return ret;

	return ret;
}
