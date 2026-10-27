
#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <arpa/inet.h>
#include <errno.h>
#include <linux/if.h>
#include <linux/if_addr.h>
#include <linux/if_link.h>
#include <linux/if_packet.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <linux/types.h>
#include <net/ethernet.h>
#include <netinet/in.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "../../shared/cgo/compiler.h"
#include "../../shared/cgo/macro.h"

#ifndef NETNS_RTA
#define NETNS_RTA(r) \
	((struct rtattr *)(((char *)(r)) + NLMSG_ALIGN(sizeof(struct rtgenmsg))))
#endif

#define IFADDRS_HASH_SIZE 64

#define __NETLINK_ALIGN(len) (((len) + 3) & ~3)

#define __NLMSG_OK(nlh, end) \
	((char *)(end) - (char *)(nlh) >= sizeof(struct nlmsghdr))

#define __NLMSG_NEXT(nlh) \
	(struct nlmsghdr *)((char *)(nlh) + __NETLINK_ALIGN((nlh)->nlmsg_len))

#define __NLMSG_DATA(nlh) ((void *)((char *)(nlh) + sizeof(struct nlmsghdr)))

#define __NLMSG_DATAEND(nlh) ((char *)(nlh) + (nlh)->nlmsg_len)

#define __NLMSG_RTA(nlh, len)                               \
	((void *)((char *)(nlh) + sizeof(struct nlmsghdr) + \
		  __NETLINK_ALIGN(len)))

#define __RTA_DATALEN(rta) ((rta)->rta_len - sizeof(struct rtattr))

#define __RTA_NEXT(rta) \
	(struct rtattr *)((char *)(rta) + __NETLINK_ALIGN((rta)->rta_len))

#define __RTA_OK(nlh, end) \
	((char *)(end) - (char *)(rta) >= sizeof(struct rtattr))

#define __NLMSG_RTAOK(rta, nlh) __RTA_OK(rta, __NLMSG_DATAEND(nlh))

#define __IN6_IS_ADDR_LINKLOCAL(a) \
	((((uint8_t *)(a))[0]) == 0xfe && (((uint8_t *)(a))[1] & 0xc0) == 0x80)

#define __IN6_IS_ADDR_MC_LINKLOCAL(a) \
	(IN6_IS_ADDR_MULTICAST(a) && ((((uint8_t *)(a))[1] & 0xf) == 0x2))

#define __RTA_DATA(rta) ((void *)((char *)(rta) + sizeof(struct rtattr)))


#define NLMSG_TAIL(nmsg)                      \
	((struct rtattr *)(((void *)(nmsg)) + \
			   __NETLINK_ALIGN((nmsg)->nlmsg_len)))

static int netlink_open(int protocol)
{
	int fd, ret;
	socklen_t socklen;
	struct sockaddr_nl local;
	int sndbuf = 32768;
	int rcvbuf = 32768;
	int err = -1;

	fd = socket(AF_NETLINK, SOCK_RAW, protocol);
	if (fd < 0)
		return -1;

	ret = setsockopt(fd, SOL_SOCKET, SO_SNDBUF, &sndbuf, sizeof(sndbuf));
	if (ret < 0)
		goto out;

	ret = setsockopt(fd, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
	if (ret < 0)
		goto out;

	memset(&local, 0, sizeof(local));
	local.nl_family = AF_NETLINK;
	local.nl_groups = 0;

	ret = bind(fd, (struct sockaddr *)&local, sizeof(local));
	if (ret < 0)
		goto out;

	socklen = sizeof(local);
	ret = getsockname(fd, (struct sockaddr *)&local, &socklen);
	if (ret < 0)
		goto out;

	errno = -EINVAL;
	if (socklen != sizeof(local))
		goto out;

	errno = -EINVAL;
	if (local.nl_family != AF_NETLINK)
		goto out;

	return fd;

out:
	close(fd);
	return err;
}

static int netlink_recv(int fd, struct nlmsghdr *nlmsghdr)
{
	int ret;
	struct sockaddr_nl nladdr;
	struct iovec iov = {
	    .iov_base = nlmsghdr,
	    .iov_len = nlmsghdr->nlmsg_len,
	};

	struct msghdr msg = {
	    .msg_name = &nladdr,
	    .msg_namelen = sizeof(nladdr),
	    .msg_iov = &iov,
	    .msg_iovlen = 1,
	};

	memset(&nladdr, 0, sizeof(nladdr));
	nladdr.nl_family = AF_NETLINK;
	nladdr.nl_pid = 0;
	nladdr.nl_groups = 0;

again:
	ret = recvmsg(fd, &msg, 0);
	if (ret < 0) {
		if (errno == EINTR)
			goto again;

		return -1;
	}

	if (!ret)
		return 0;

	if (msg.msg_flags & MSG_TRUNC && ((__u32)ret == nlmsghdr->nlmsg_len)) {
		errno = EMSGSIZE;
		ret = -1;
	}

	return ret;
}

static int __netlink_send(int fd, struct nlmsghdr *nlmsghdr)
{
	int ret;
	struct sockaddr_nl nladdr;
	struct iovec iov = {
	    .iov_base = nlmsghdr,
	    .iov_len = nlmsghdr->nlmsg_len,
	};
	struct msghdr msg = {
	    .msg_name = &nladdr,
	    .msg_namelen = sizeof(nladdr),
	    .msg_iov = &iov,
	    .msg_iovlen = 1,
	};

	memset(&nladdr, 0, sizeof(nladdr));
	nladdr.nl_family = AF_NETLINK;
	nladdr.nl_pid = 0;
	nladdr.nl_groups = 0;

	ret = sendmsg(fd, &msg, MSG_NOSIGNAL);
	if (ret < 0)
		return -1;

	return ret;
}

static int netlink_transaction(int fd, struct nlmsghdr *request,
			       struct nlmsghdr *answer)
{
	int ret;

	ret = __netlink_send(fd, request);
	if (ret < 0)
		return -1;

	ret = netlink_recv(fd, answer);
	if (ret < 0)
		return -1;

	ret = 0;
	if (answer->nlmsg_type == NLMSG_ERROR) {
		struct nlmsgerr *err = (struct nlmsgerr *)__NLMSG_DATA(answer);
		errno = -err->error;
		if (err->error < 0)
			ret = -1;
	}

	return ret;
}

static int parse_rtattr(struct rtattr *tb[], int max, struct rtattr *rta, int len)
{
	memset(tb, 0, sizeof(struct rtattr *) * (max + 1));

	while (RTA_OK(rta, len)) {
		unsigned short type = rta->rta_type;

		if ((type <= max) && (!tb[type]))
			tb[type] = rta;

		rta = RTA_NEXT(rta, len);
	}

	return 0;
}

static __s32 rta_getattr_s32(const struct rtattr *rta)
{
	return *(__s32 *)RTA_DATA(rta);
}

static int addattr(struct nlmsghdr *n, size_t maxlen, int type,
		   const void *data, size_t alen)
{
	int len = RTA_LENGTH(alen);
	struct rtattr *rta;

	if (NLMSG_ALIGN(n->nlmsg_len) + RTA_ALIGN(len) > maxlen)
		return -1;

	rta = NLMSG_TAIL(n);
	rta->rta_type = type;
	rta->rta_len = len;
	if (alen)
		memcpy(RTA_DATA(rta), data, alen);
	n->nlmsg_len = NLMSG_ALIGN(n->nlmsg_len) + RTA_ALIGN(len);

	return 0;
}

__unused static __s32 netns_get_nsid(__s32 netns_fd)
{
	int fd, ret;
	ssize_t len;
	char buf[NLMSG_ALIGN(sizeof(struct nlmsghdr)) +
		 NLMSG_ALIGN(sizeof(struct rtgenmsg)) + NLMSG_ALIGN(1024)];
	struct rtattr *tb[__LXC_NETNSA_MAX + 1];
	struct nlmsghdr *hdr;
	struct rtgenmsg *msg;
	int saved_errno;

	fd = netlink_open(NETLINK_ROUTE);
	if (fd < 0)
		return -1;

	memset(buf, 0, sizeof(buf));
	hdr = (struct nlmsghdr *)buf;
	msg = (struct rtgenmsg *)__NLMSG_DATA(hdr);

	hdr->nlmsg_len = NLMSG_LENGTH(sizeof(*msg));
	hdr->nlmsg_type = RTM_GETNSID;
	hdr->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
	hdr->nlmsg_pid = 0;
	hdr->nlmsg_seq = RTM_GETNSID;
	msg->rtgen_family = AF_UNSPEC;

	addattr(hdr, 1024, __LXC_NETNSA_FD, &netns_fd, sizeof(__s32));

	ret = netlink_transaction(fd, hdr, hdr);
	saved_errno = errno;
	close(fd);
	errno = saved_errno;
	if (ret < 0)
		return -1;

	msg = __NLMSG_DATA(hdr);
	len = hdr->nlmsg_len - NLMSG_SPACE(sizeof(*msg));
	if (len < 0)
		return -1;

	parse_rtattr(tb, __LXC_NETNSA_MAX, NETNS_RTA(msg), len);
	if (tb[__LXC_NETNSA_NSID])
		return rta_getattr_s32(tb[__LXC_NETNSA_NSID]);

	return -1;
}
