#ifndef __INCUS_PROCESS_UTILS_H
#define __INCUS_PROCESS_UTILS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <linux/sched.h>
#include <sched.h>
#include <inttypes.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

#include "compiler.h"
#include "file_utils.h"
#include "memory_utils.h"
#include "syscall_numbers.h"

#ifndef PIDFD_THREAD
#define PIDFD_THREAD O_EXCL
#endif

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

static inline size_t strlcpy(char *dest, const char *src, size_t size)
{
	size_t ret = strlen(src);

	if (size) {
		size_t len = (ret >= size) ? size - 1 : ret;
		memcpy(dest, src, len);
		dest[len] = '\0';
	}

	return ret;
}

/*
 * Sets the process title to the specified title. Note that this may fail if
 * the kernel doesn't support PR_SET_MM_MAP (kernels <3.18).
 */
static inline int setproctitle(char *title)
{
	__do_fclose FILE *f = NULL;
	int i, fd, len;
	char *buf_ptr, *tmp_proctitle;
	char buf[LXC_LINELEN];
	int ret = 0;
	ssize_t bytes_read = 0;
	static char *proctitle = NULL;

	/*
	 * We don't really need to know all of this stuff, but unfortunately
	 * PR_SET_MM_MAP requires us to set it all at once, so we have to
	 * figure it out anyway.
	 */
	uint64_t start_data, end_data, start_brk, start_code, end_code,
	    start_stack, arg_start, arg_end, env_start, env_end, brk_val;
	struct prctl_mm_map prctl_map;

	f = fopen("/proc/self/stat", "r");
	if (!f)
		return -1;

	fd = fileno(f);
	if (fd < 0)
		return -1;

	bytes_read = read_nointr(fd, buf, sizeof(buf) - 1);
	if (bytes_read <= 0)
		return -1;

	buf[bytes_read] = '\0';

	/*
	 * executable names may contain spaces, so we search backwards for the
	 * ), which is the kernel's marker for "end of executable name". this
	 * puts the pointer at the end of the second field.
	 */
	buf_ptr = strrchr(buf, ')');
	if (!buf_ptr)
		return -1;

	/* Skip the space and the next 23 fields, column 26-28 are start_code,
         * end_code, and start_stack */
	for (i = 0; i < 24; i++) {
		buf_ptr = strchr(buf_ptr + 1, ' ');
		if (!buf_ptr)
			return -1;
	}

	i = sscanf(buf_ptr, "%" PRIu64 " %" PRIu64 " %" PRIu64, &start_code, &end_code, &start_stack);
	if (i != 3)
		return -1;

	/* Skip the next 19 fields, column 45-51 are start_data to arg_end */
	for (i = 0; i < 19; i++) {
		buf_ptr = strchr(buf_ptr + 1, ' ');
		if (!buf_ptr)
			return -1;
	}

	i = sscanf(buf_ptr, "%" PRIu64 " %" PRIu64 " %" PRIu64 " %*u %*u %" PRIu64 " %" PRIu64, &start_data,
		   &end_data, &start_brk, &env_start, &env_end);
	if (i != 5)
		return -1;

	/* Include the null byte here, because in the calculations below we
	 * want to have room for it. */
	len = strlen(title) + 1;

	tmp_proctitle = realloc(proctitle, len);
	if (!tmp_proctitle)
		return -1;

	proctitle = tmp_proctitle;

	arg_start = (unsigned long)proctitle;
	arg_end = arg_start + len;

	brk_val = syscall(__NR_brk, 0);

	prctl_map = (struct prctl_mm_map){
	    .start_code = start_code,
	    .end_code = end_code,
	    .start_stack = start_stack,
	    .start_data = start_data,
	    .end_data = end_data,
	    .start_brk = start_brk,
	    .brk = brk_val,
	    .arg_start = arg_start,
	    .arg_end = arg_end,
	    .env_start = env_start,
	    .env_end = env_end,
	    .auxv = NULL,
	    .auxv_size = 0,
	    .exe_fd = -1,
	};

	ret = prctl(PR_SET_MM, prctl_arg(PR_SET_MM_MAP), prctl_arg(&prctl_map),
		    prctl_arg(sizeof(prctl_map)), prctl_arg(0));
	if (ret == 0)
		(void)strlcpy((char *)arg_start, title, len);

	return ret;
}

#endif /* __INCUS_PROCESS_UTILS_H */
