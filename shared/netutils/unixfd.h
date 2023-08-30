
#ifndef NETUTILS_UNIXFD_H
#define NETUTILS_UNIXFD_H

#include <linux/types.h>
#include <sys/socket.h>
#include <sys/types.h>

#define KERNEL_SCM_MAX_FD 253

/* Allow the caller to set expectations. */
#define UNIX_FDS_ACCEPT_EXACT	((__u32)(1 << 0)) /* default */
#define UNIX_FDS_ACCEPT_LESS	((__u32)(1 << 1))
#define UNIX_FDS_ACCEPT_MORE	((__u32)(1 << 2)) /* wipe any extra fds */
#define UNIX_FDS_ACCEPT_NONE	((__u32)(1 << 3))
#define UNIX_FDS_ACCEPT_MASK (UNIX_FDS_ACCEPT_EXACT | UNIX_FDS_ACCEPT_LESS | UNIX_FDS_ACCEPT_MORE | UNIX_FDS_ACCEPT_NONE)

/* Allow the callee to disappoint them. */
#define UNIX_FDS_RECEIVED_EXACT	((__u32)(1 << 16))
#define UNIX_FDS_RECEIVED_LESS	((__u32)(1 << 17))
#define UNIX_FDS_RECEIVED_MORE	((__u32)(1 << 18))
#define UNIX_FDS_RECEIVED_NONE	((__u32)(1 << 19))

struct unix_fds {
	__u32 fd_count_max;
	__u32 fd_count_ret;
	__u32 flags;
	__s32 fd[KERNEL_SCM_MAX_FD];
} __attribute__((aligned(8)));

extern int lxc_abstract_unix_send_fds(int fd, int *sendfds, int num_sendfds,
				      void *data, size_t size);

extern ssize_t lxc_abstract_unix_recv_fds_iov(int fd, struct unix_fds *ret_fds,
					      struct iovec *ret_iov,
					      size_t size_ret_iov);

extern ssize_t lxc_abstract_unix_recv_fds(int fd, struct unix_fds *ret_fds,
					  void *ret_data, size_t size_ret_data);

#endif // NETUTILS_UNIXFD_H
