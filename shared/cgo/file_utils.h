#ifndef __INCUS_FILE_UTILS_H
#define __INCUS_FILE_UTILS_H

#include "config.h"

#include <stdio.h>
#include <stdlib.h>
#include <sys/types.h>
#include <unistd.h>

static inline ssize_t read_nointr(int fd, void *buf, size_t count)
{
	ssize_t ret;

	do {
		ret = read(fd, buf, count);
	} while (ret < 0 && errno == EINTR);

	return ret;
}

static inline ssize_t write_nointr(int fd, const void *buf, size_t count)
{
	ssize_t ret;

	do {
		ret = write(fd, buf, count);
	} while (ret < 0 && errno == EINTR);

	return ret;
}

#endif /* __INCUS_FILE_UTILS_H */
