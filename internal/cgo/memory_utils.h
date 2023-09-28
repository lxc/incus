#ifndef __LXC_MEMORY_UTILS_H
#define __LXC_MEMORY_UTILS_H

#include <dirent.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/types.h>
#include <unistd.h>

#include "macro.h"

#define define_cleanup_function(type, cleaner)           \
	static inline void cleaner##_function(type *ptr) \
	{                                                \
		if (*ptr)                                \
			cleaner(*ptr);                   \
	}

#define call_cleaner(cleaner) __attribute__((__cleanup__(cleaner##_function)))

#define close_prot_errno_disarm(fd) \
	if (fd >= 0) {              \
		int _e_ = errno;    \
		close(fd);          \
		errno = _e_;        \
		fd = -EBADF;        \
	}

static inline void close_prot_errno_disarm_function(int *fd)
{
       close_prot_errno_disarm(*fd);
}
#define __do_close call_cleaner(close_prot_errno_disarm)

define_cleanup_function(FILE *, fclose);
#define __do_fclose call_cleaner(fclose)

define_cleanup_function(DIR *, closedir);
#define __do_closedir call_cleaner(closedir)

#define free_disarm(ptr)       \
	({                     \
		free(ptr);     \
		move_ptr(ptr); \
	})

static inline void free_disarm_function(void *ptr)
{
	free_disarm(*(void **)ptr);
}
#define __do_free call_cleaner(free_disarm)

static inline void free_string_list(char **list)
{
	if (list) {
		for (int i = 0; list[i]; i++)
			free(list[i]);
		free_disarm(list);
	}
}
define_cleanup_function(char **, free_string_list);
#define __do_free_string_list call_cleaner(free_string_list)

#define zalloc(__size__) (calloc(1, __size__))

#endif /* __LXC_MEMORY_UTILS_H */
