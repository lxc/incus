/* liblxcapi
 *
 * Copyright © 2018 Christian Brauner <christian.brauner@ubuntu.com>.
 * Copyright © 2018 Canonical Ltd.
 *
 * This library is free software; you can redistribute it and/or
 * modify it under the terms of the GNU Lesser General Public
 * License as published by the Free Software Foundation; either
 * version 2.1 of the License, or (at your option) any later version.

 * This library is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
 * Lesser General Public License for more details.

 * You should have received a copy of the GNU Lesser General Public License
 * along with this library; if not, write to the Free Software Foundation,
 * Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA
 */

#ifndef __LXC_MACRO_H
#define __LXC_MACRO_H

#include <asm/types.h>
#include <limits.h>
#include <linux/if_link.h>
#include <linux/loop.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <linux/types.h>
#include <stdint.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>
#include <sys/wait.h>

#ifndef PATH_MAX
#define PATH_MAX 4096
#endif

/* Calculate the number of chars needed to represent a given integer as a C
 * string. Include room for '-' to indicate negative numbers and the \0 byte.
 * This is based on systemd.
 */
#define INTTYPE_TO_STRLEN(type)                   \
	(2 + (sizeof(type) <= 1                   \
		  ? 3                             \
		  : sizeof(type) <= 2             \
			? 5                       \
			: sizeof(type) <= 4       \
			      ? 10                \
			      : sizeof(type) <= 8 \
				    ? 20          \
				    : sizeof(int[-2 * (sizeof(type) > 8)])))

/* Useful macros */
#define LXC_LINELEN 4096
#define LXC_IDMAPLEN 4096
#define LXC_MAX_BUFFER 4096

/* /proc/       =    6
 *                +
 * <pid-as-str> =   INTTYPE_TO_STRLEN(pid_t)
 *                +
 * /fd/         =    4
 *                +
 * <fd-as-str>  =   INTTYPE_TO_STRLEN(int)
 *                +
 * \0           =    1
 */
#define LXC_PROC_PID_FD_LEN \
	(6 + INTTYPE_TO_STRLEN(pid_t) + 4 + INTTYPE_TO_STRLEN(int) + 1)

/* /proc/        = 6
 *               +
 * <pid-as-str>  = INTTYPE_TO_STRLEN(pid_t)
 *               +
 * /status       = 7
 *               +
 * \0            = 1
 */
#define LXC_PROC_STATUS_LEN (6 + INTTYPE_TO_STRLEN(pid_t) + 7 + 1)

/* loop devices */
#ifndef LO_FLAGS_AUTOCLEAR
#define LO_FLAGS_AUTOCLEAR 4
#endif

#ifndef LOOP_CTL_GET_FREE
#define LOOP_CTL_GET_FREE 0x4C82
#endif

/* memfd_create() */
#ifndef MFD_CLOEXEC
#define MFD_CLOEXEC 0x0001U
#endif

#ifndef MFD_ALLOW_SEALING
#define MFD_ALLOW_SEALING 0x0002U
#endif

/**
 * BUILD_BUG_ON - break compile if a condition is true.
 * @condition: the condition which the compiler should know is false.
 *
 * If you have some code which relies on certain constants being equal, or
 * other compile-time-evaluated condition, you should use BUILD_BUG_ON to
 * detect if someone changes it.
 *
 * The implementation uses gcc's reluctance to create a negative array, but
 * gcc (as of 4.4) only emits that error for obvious cases (eg. not arguments
 * to inline functions).  So as a fallback we use the optimizer; if it can't
 * prove the condition is false, it will cause a link error on the undefined
 * "__build_bug_on_failed".  This error message can be harder to track down
 * though, hence the two different methods.
 */
#ifndef __OPTIMIZE__
#define BUILD_BUG_ON(condition) ((void)sizeof(char[1 - 2 * !!(condition)]))
#else
extern int __build_bug_on_failed;
#define BUILD_BUG_ON(condition)                              \
	do {                                                 \
		((void)sizeof(char[1 - 2 * !!(condition)])); \
		if (condition)                               \
			__build_bug_on_failed = 1;           \
	} while (0)
#endif

#define lxc_iterate_parts(__iterator, __splitme, __separators)                  \
	for (char *__p = NULL, *__it = strtok_r(__splitme, __separators, &__p); \
	     (__iterator = __it);                                               \
	     __iterator = __it = strtok_r(NULL, __separators, &__p))

#define prctl_arg(x) ((unsigned long)x)

/* networking */
#ifndef NETLINK_GET_STRICT_CHK
#define NETLINK_GET_STRICT_CHK 12
#endif

#ifndef SOL_NETLINK
#define SOL_NETLINK 270
#endif

#ifndef IFLA_LINKMODE
#define IFLA_LINKMODE 17
#endif

#ifndef IFLA_LINKINFO
#define IFLA_LINKINFO 18
#endif

#ifndef IFLA_NET_NS_PID
#define IFLA_NET_NS_PID 19
#endif

#ifndef IFLA_INFO_KIND
#define IFLA_INFO_KIND 1
#endif

#ifndef IFLA_VLAN_ID
#define IFLA_VLAN_ID 1
#endif

#ifndef IFLA_INFO_DATA
#define IFLA_INFO_DATA 2
#endif

#ifndef VETH_INFO_PEER
#define VETH_INFO_PEER 1
#endif

#ifndef IFLA_MACVLAN_MODE
#define IFLA_MACVLAN_MODE 1
#endif

#ifndef IFLA_NEW_NETNSID
#define IFLA_NEW_NETNSID 45
#endif

#ifdef IFLA_IF_NETNSID
#ifndef IFLA_TARGET_NETNSID
#define IFLA_TARGET_NETNSID = IFLA_IF_NETNSID
#endif
#else
#define IFLA_IF_NETNSID 46
#define IFLA_TARGET_NETNSID 46
#endif

#ifndef IFA_TARGET_NETNSID
#define IFA_TARGET_NETNSID 10
#endif

#ifndef IFLA_STATS
#define IFLA_STATS 7
#endif

#ifndef IFLA_STATS64
#define IFLA_STATS64 23
#endif

#ifndef RTM_NEWNSID
#define RTM_NEWNSID 88
#endif

#ifndef RTM_GETNSID
#define RTM_GETNSID 90
#endif

#ifndef NLMSG_ERROR
#define NLMSG_ERROR 0x2
#endif

/* Attributes of RTM_NEWNSID/RTM_GETNSID messages */
enum {
	__LXC_NETNSA_NONE,
#define __LXC_NETNSA_NSID_NOT_ASSIGNED -1
	__LXC_NETNSA_NSID,
	__LXC_NETNSA_PID,
	__LXC_NETNSA_FD,
	__LXC_NETNSA_MAX,
};

/* Length of abstract unix domain socket socket address. */
#define LXC_AUDS_ADDR_LEN sizeof(((struct sockaddr_un *)0)->sun_path)

/* pointer conversion macros */
#define PTR_TO_INT(p) ((int)((intptr_t)(p)))
#define INT_TO_PTR(u) ((void *)((intptr_t)(u)))

#define PTR_TO_INTMAX(p) ((intmax_t)((intptr_t)(p)))
#define INTMAX_TO_PTR(u) ((void *)((intptr_t)(u)))

#define LXC_INVALID_UID ((uid_t)-1)
#define LXC_INVALID_GID ((gid_t)-1)

#define STRLITERALLEN(x) (sizeof(""x"") - 1)
#define STRARRAYLEN(x) (sizeof(x) - 1)

/* Maximum number of bytes sendfile() is able to send in one go. */
#define LXC_SENDFILE_MAX 0x7ffff000

#define move_ptr(ptr)                                 \
	({                                            \
		typeof(ptr) __internal_ptr__ = (ptr); \
		(ptr) = NULL;                         \
		__internal_ptr__;                     \
	})

#define move_fd(fd)                         \
	({                                  \
		int __internal_fd__ = (fd); \
		(fd) = -EBADF;              \
		__internal_fd__;            \
	})

#define ret_errno(__errno__)         \
	({                           \
		errno = (__errno__); \
		-(__errno__);        \
	})

#define log_error(__ret__, format, ...)                       \
	({                                                    \
		typeof(__ret__) __internal_ret__ = (__ret__); \
		fprintf(stderr, format "\n", ##__VA_ARGS__);  \
		__internal_ret__;                             \
	})

#define log_errno(__ret__, format, ...)                       \
	({                                                    \
		typeof(__ret__) __internal_ret__ = (__ret__); \
		fprintf(stderr, "%m - " format "\n", ##__VA_ARGS__);  \
		__internal_ret__;                             \
	})

#ifndef P_PIDFD
#define P_PIDFD 3
#endif

#define ARRAY_SIZE(arr) (sizeof(arr) / sizeof((arr)[0]))

#ifndef CLONE_NEWTIME
#define CLONE_NEWTIME 0x00000080
#endif

#ifndef CLONE_NEWCGROUP
#define CLONE_NEWCGROUP	0x02000000
#endif

#define BUILD_BUG_ON_ZERO(e) ((int)(sizeof(struct { int:(-!!(e)); })))

/*
 * Compile time versions of __arch_hweightN()
 */
#define __const_hweight8(w)		\
	((unsigned int)			\
	 ((!!((w) & (1ULL << 0))) +	\
	  (!!((w) & (1ULL << 1))) +	\
	  (!!((w) & (1ULL << 2))) +	\
	  (!!((w) & (1ULL << 3))) +	\
	  (!!((w) & (1ULL << 4))) +	\
	  (!!((w) & (1ULL << 5))) +	\
	  (!!((w) & (1ULL << 6))) +	\
	  (!!((w) & (1ULL << 7)))))

#define __const_hweight16(w) (__const_hweight8(w)  + __const_hweight8((w)  >> 8 ))
#define __const_hweight32(w) (__const_hweight16(w) + __const_hweight16((w) >> 16))
#define __const_hweight64(w) (__const_hweight32(w) + __const_hweight32((w) >> 32))

#define hweight8(w) __const_hweight8(w)
#define hweight16(w) __const_hweight16(w)
#define hweight32(w) __const_hweight32(w)
#define hweight64(w) __const_hweight64(w)

/*
 * /proc/           = 6
 *                  +
 * <pid-as_str>     = INTTYPE_TO_STRLEN(pid_t)
 *                  +
 * /.id_map         = 8
 *                  +
 * \0               = 1
 */
#define PROC_PID_IDMAP_LEN (6 + INTTYPE_TO_STRLEN(pid_t) + 8 + 1)

#define strnprintf(buf, buf_size, ...)                                                    \
	({                                                                                \
		int __ret_strnprintf;                                                     \
		__ret_strnprintf = snprintf(buf, buf_size, ##__VA_ARGS__);                \
		if (__ret_strnprintf < 0 || (size_t)__ret_strnprintf >= (size_t)buf_size) \
			__ret_strnprintf = ret_errno(EIO);				  \
		__ret_strnprintf;                                                         \
	})

#define STRINGIFY(a) __STRINGIFY(a)
#define __STRINGIFY(a) #a

#define log_stderr(format, ...)                                               \
	fprintf(stderr, "%s: %d: %s - %m - " format "\n", __FILE__, __LINE__, \
		__func__, ##__VA_ARGS__)

#define die_errno(__errno__, format, ...)          \
	({                                         \
		errno = (__errno__);               \
		log_stderr(format, ##__VA_ARGS__); \
		_exit(EXIT_FAILURE);               \
	})

#define die(format, ...) die_errno(errno, format, ##__VA_ARGS__)

#endif /* __LXC_MACRO_H */
