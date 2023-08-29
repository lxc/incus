#ifndef __INCUS_PROCESS_UTILS_H
#define __INCUS_PROCESS_UTILS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/sched.h>
#include <sched.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

#include "compiler.h"
#include "memory_utils.h"
#include "syscall_numbers.h"

static inline int incus_pidfd_open(pid_t pid, unsigned int flags)
{
	return syscall(__NR_pidfd_open, pid, flags);
}

static inline int incus_pidfd_send_signal(int pidfd, int sig, siginfo_t *info,
					unsigned int flags)
{
	return syscall(__NR_pidfd_send_signal, pidfd, sig, info, flags);
}

static inline bool process_still_alive(int pidfd)
{
	return incus_pidfd_send_signal(pidfd, 0, NULL, 0) == 0;
}

static inline int wait_for_pid(pid_t pid)
{
	int status, ret;

again:
	ret = waitpid(pid, &status, 0);
	if (ret == -1) {
		if (errno == EINTR)
			goto again;
		return -1;
	}
	if (ret != pid)
		goto again;
	if (!WIFEXITED(status) || WEXITSTATUS(status) != 0)
		return -1;
	return 0;
}

static inline int wait_for_pid_status_nointr(pid_t pid)
{
	int status, ret;

again:
	ret = waitpid(pid, &status, 0);
	if (ret == -1) {
		if (errno == EINTR)
			goto again;

		return -1;
	}

	if (ret != pid)
		goto again;

	return status;
}

static inline int append_null_to_list(void ***list)
{
	int newentry = 0;
	void **new_list;

	if (*list)
		for (; (*list)[newentry]; newentry++)
			;

	new_list = realloc(*list, (newentry + 2) * sizeof(void **));
	if (!new_list)
		return ret_errno(ENOMEM);

	*list = new_list;
	(*list)[newentry + 1] = NULL;
	return newentry;
}

static inline int push_vargs(char ***list, char *entry)
{
	__do_free char *copy = NULL;
	int newentry;

	copy = strdup(entry);
	if (!copy)
		return ret_errno(ENOMEM);

	newentry = append_null_to_list((void ***)list);
	if (newentry < 0)
		return newentry;

	(*list)[newentry] = move_ptr(copy);

	return 0;
}

#endif /* __INCUS_PROCESS_UTILS_H */
