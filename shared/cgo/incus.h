#ifndef INCUS_H
#define INCUS_H

#include "compiler.h"

__hidden extern char *advance_arg(bool required);
__hidden extern void attach_userns_fd(int ns_fd);
__hidden extern int can_inject_uevent(const char *uevent, size_t len);
__hidden extern void checkfeature();
__hidden extern bool change_namespaces(int pidfd, int nsfd, unsigned int flags);
__hidden extern int close_inherited(int *fds_to_ignore, size_t len_fds);
__hidden extern void error(char *msg);
__hidden extern void forkcoresched();
__hidden extern void forkexec();
__hidden extern void forkfile();
__hidden extern void forkmount();
__hidden extern void forknet();
__hidden extern void forkproxy();
__hidden extern void forksyscall();
__hidden extern void forkuevent();
__hidden extern int mount_detach_idmap(const char *path, int fd_userns);
__hidden extern int pidfd_nsfd(int pidfd, pid_t pid);
__hidden extern int preserve_ns(pid_t pid, int ns_fd, const char *ns);

extern bool pidfd_setns_aware;

#endif /* INCUS_H */
