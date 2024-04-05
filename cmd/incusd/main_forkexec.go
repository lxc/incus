package main

/*
#include "config.h"

#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
#include <limits.h>

#include "incus.h"
#include "file_utils.h"
#include "macro.h"
#include "memory_utils.h"
#include "process_utils.h"
#include "syscall_wrappers.h"
#include <lxc/attach_options.h>
#include <lxc/lxccontainer.h>

define_cleanup_function(struct lxc_container *, lxc_container_put);

static int fd_cloexec(int fd, bool cloexec)
{
	int oflags, nflags;

	oflags = fcntl(fd, F_GETFD, 0);
	if (oflags < 0)
		return -errno;

	if (cloexec)
		nflags = oflags | FD_CLOEXEC;
	else
		nflags = oflags & ~FD_CLOEXEC;

	if (nflags == oflags)
		return 0;

	if (fcntl(fd, F_SETFD, nflags) < 0)
		return -errno;

	return 0;
}

static int safe_int(const char *numstr, int *converted)
{
	char *err = NULL;
	signed long int sli;

	errno = 0;
	sli = strtol(numstr, &err, 0);
	if (errno == ERANGE && (sli == LONG_MAX || sli == LONG_MIN))
		return -ERANGE;

	if (errno != 0 && sli == 0)
		return -EINVAL;

	if (err == numstr || *err != '\0')
		return -EINVAL;

	if (sli > INT_MAX || sli < INT_MIN)
		return -ERANGE;

	*converted = (int)sli;
	return 0;
}

static inline bool match_stdfds(int fd)
{
	return (fd == STDIN_FILENO || fd == STDOUT_FILENO || fd == STDERR_FILENO);
}

int close_inherited(int *fds_to_ignore, size_t len_fds)
{
	int fddir;
	DIR *dir;
	struct dirent *direntp;

restart:
	dir = opendir("/proc/self/fd");
	if (!dir)
		return -errno;

	fddir = dirfd(dir);

	while ((direntp = readdir(dir))) {
		int fd, ret;
		size_t i;

		if (strcmp(direntp->d_name, ".") == 0)
			continue;

		if (strcmp(direntp->d_name, "..") == 0)
			continue;

		ret = safe_int(direntp->d_name, &fd);
		if (ret < 0)
			continue;

		for (i = 0; i < len_fds; i++)
			if (fds_to_ignore[i] == fd)
				break;

		if (fd == fddir || (i < len_fds && fd == fds_to_ignore[i]))
			continue;

		if (match_stdfds(fd))
			continue;

		if (close(fd)) {
			return log_error(-errno, "%s - Failed to close file descriptor %d", strerror(errno), fd);
		} else {
			char fdpath[PATH_MAX], realpath[PATH_MAX];

			snprintf(fdpath, sizeof(fdpath), "/proc/self/fd/%d", fd);
			ret = readlink(fdpath, realpath, PATH_MAX);
			if (ret < 0)
				snprintf(realpath, sizeof(realpath), "unknown");
			else if (ret >= sizeof(realpath))
				realpath[sizeof(realpath) - 1] = '\0';

			log_error(-errno, "Closing unexpected file descriptor %d -> %s", fd, realpath);
		}

		closedir(dir);
		goto restart;
	}

	closedir(dir);
	return 0;
}

#define EXEC_STDIN_FD 3
#define EXEC_STDOUT_FD 4
#define EXEC_STDERR_FD 5
#define EXEC_PIPE_FD 6
#define ARRAY_SIZE(arr) (sizeof(arr) / sizeof((arr)[0]))

// We use a separate function because cleanup macros are called during stack
// unwinding if I'm not mistaken and if the compiler knows it exits it won't
// call them. That's not a problem since we're exiting but I just like to be on
// the safe side in case we ever call this from a different context. We also
// tell the compiler to not inline us.
__attribute__ ((noinline)) static int __forkexec(void)
{
	__do_close int status_pipe = EXEC_PIPE_FD;
	__do_free_string_list char **argvp = NULL, **envvp = NULL;
	call_cleaner(lxc_container_put) struct lxc_container *c = NULL;
	const char *config_path = NULL, *lxcpath = NULL, *name = NULL;
	char *cwd = NULL;
	pid_t init_pid;
	lxc_attach_options_t attach_options = LXC_ATTACH_OPTIONS_DEFAULT;
	lxc_attach_command_t command = {
		.program = NULL,
	};
	int fds_to_ignore[] = {EXEC_STDIN_FD, EXEC_STDOUT_FD, EXEC_STDERR_FD, EXEC_PIPE_FD};
	ssize_t ret;
	pid_t attached_pid;
	uid_t uid;
	gid_t gid;
	int coresched;

	if (geteuid() != 0)
		return log_error(EXIT_FAILURE, "Error: forkexec requires root privileges");

	name = advance_arg(false);
	if (name == NULL ||
	    (strcmp(name, "--help") == 0 ||
	     strcmp(name, "--version") == 0 || strcmp(name, "-h") == 0))
		return 0;

	lxcpath = advance_arg(true);
	config_path = advance_arg(true);
	cwd = advance_arg(true);
	uid = atoi(advance_arg(true));
	if (uid < 0)
		uid = (uid_t) - 1;
	gid = atoi(advance_arg(true));
	if (gid < 0)
		gid = (gid_t) - 1;
	coresched = atoi(advance_arg(true));
	if (coresched != 0 && coresched != 1)
		_exit(EXIT_FAILURE);

	for (char *arg = NULL, *section = NULL; (arg = advance_arg(false)); ) {
		if (!strcmp(arg, "--") && (!section || strcmp(section, "cmd"))) {
			section = NULL;
			continue;
		}

		if (!section) {
			section = arg;
			continue;
		}

		if (!strcmp(section, "env")) {
			if (!strncmp(arg, "HOME=", STRLITERALLEN("HOME=")))
				attach_options.initial_cwd = arg + STRLITERALLEN("HOME=");
			ret = push_vargs(&envvp, arg);
			if (ret < 0)
				return log_error(ret, "Failed to add %s to env array", arg);
		} else if (!strcmp(section, "cmd")) {
			ret = push_vargs(&argvp, arg);
			if (ret < 0)
				return log_error(ret, "Failed to add %s to arg array", arg);
		} else {
			return log_error(EXIT_FAILURE, "Invalid exec section %s", section);
		}
	}

	if (!argvp || !*argvp)
		return log_error(EXIT_FAILURE, "No command specified");

	ret = incus_close_range(EXEC_PIPE_FD + 1, UINT_MAX, CLOSE_RANGE_UNSHARE);
	if (ret) {
		// Fallback to close_inherited() when the syscall is not
		// available or when CLOSE_RANGE_UNSHARE isn't supported.
		// On a regular kernel CLOSE_RANGE_UNSHARE should always be
		// available but openSUSE Leap 15.3 seems to have a partial
		// backport without CLOSE_RANGE_UNSHARE support.
		if (errno == ENOSYS || errno == EINVAL)
			ret = close_inherited(fds_to_ignore, ARRAY_SIZE(fds_to_ignore));
	}

	if (ret)
		return log_error(EXIT_FAILURE, "Aborting attach to prevent leaking file descriptors into container");

	ret = fd_cloexec(status_pipe, true);
	if (ret)
		return log_errno(EXIT_FAILURE, "Failed to make pipe close-on-exec");

	c = lxc_container_new(name, lxcpath);
	if (!c)
		return log_error(EXIT_FAILURE, "Failed to load new container %s/%s", lxcpath, name);

	c->clear_config(c);

	if (!c->load_config(c, config_path))
		return log_error(EXIT_FAILURE, "Failed to load config file %s for %s/%s", config_path, lxcpath, name);

	if (strcmp(cwd, ""))
		attach_options.initial_cwd = cwd;
	attach_options.env_policy = LXC_ATTACH_CLEAR_ENV;
	attach_options.extra_env_vars = envvp;
	attach_options.stdin_fd = 3;
	attach_options.stdout_fd = 4;
	attach_options.stderr_fd = 5;
	attach_options.uid = uid;
	attach_options.gid = gid;
	command.program = argvp[0];
	command.argv = argvp;

	ret = c->attach(c, lxc_attach_run_command, &command, &attach_options, &attached_pid);
	if (ret < 0)
		return EXIT_FAILURE;

	ret = write_nointr(status_pipe, &attached_pid, sizeof(attached_pid));
	if (ret < 0) {
		// Kill the child just to be safe.
		fprintf(stderr, "Failed to send pid %d of executing child to daemon. Killing child\n", attached_pid);
		kill(attached_pid, SIGKILL);
		goto out_reap;
	}

	if (coresched == 1) {
		pid_t pid;

		init_pid = c->init_pid(c);
		if (init_pid < 0) {
			kill(attached_pid, SIGKILL);
			goto out_reap;
		}

		pid = vfork();
		if (pid < 0) {
			kill(attached_pid, SIGKILL);
			goto out_reap;
		}

		if (pid == 0) {
			__u64 cookie;

			ret = core_scheduling_cookie_share_with(init_pid);
			if (ret)
				_exit(EXIT_FAILURE);

			ret = core_scheduling_cookie_share_to(attached_pid);
			if (ret)
				_exit(EXIT_FAILURE);

			cookie = core_scheduling_cookie_get(attached_pid);
			if (!core_scheduling_cookie_valid(cookie))
				_exit(EXIT_FAILURE);

			_exit(EXIT_SUCCESS);
		}

		ret = wait_for_pid(pid);
		if (ret)
			kill(attached_pid, SIGKILL);
	}

out_reap:
	ret = wait_for_pid_status_nointr(attached_pid);
	if (ret < 0)
		return log_error(EXIT_FAILURE, "Failed to wait for child process %d", attached_pid);

	if (WIFEXITED(ret))
		return WEXITSTATUS(ret);

	if (WIFSIGNALED(ret))
		return 128 + WTERMSIG(ret);

	return EXIT_FAILURE;
}

void forkexec(void)
{
	_exit(__forkexec());
}
*/
import "C"

import (
	"fmt"

	"github.com/spf13/cobra"

	// Used by cgo
	_ "github.com/lxc/incus/v6/shared/cgo"
)

type cmdForkexec struct {
	global *cmdGlobal
}

func (c *cmdForkexec) Command() *cobra.Command {
	// Main subcommand
	cmd := &cobra.Command{}
	cmd.Use = "forkexec <container name> <containers path> <config> <cwd> <uid> <gid> <coresched> -- env [key=value...] -- cmd <args...>"
	cmd.Short = "Execute a task inside the container"
	cmd.Long = `Description:
  Execute a task inside the container

  This internal command is used to spawn a task inside the container and
  allow the daemon to interact with it.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdForkexec) Run(cmd *cobra.Command, args []string) error {
	return fmt.Errorf("This command should have been intercepted in cgo")
}
