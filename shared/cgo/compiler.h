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

#ifndef __LXC_COMPILER_H
#define __LXC_COMPILER_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif

#ifndef thread_local
#if __STDC_VERSION__ >= 201112L &&    \
    !(defined(__STDC_NO_THREADS__) || \
      (defined(__GNU_LIBRARY__) && __GLIBC__ == 2 && __GLIBC_MINOR__ < 16))
#define thread_local _Thread_local
#else
#define thread_local __thread
#endif
#endif

#ifndef __fallthrough
#define __fallthrough /* fall through */
#endif

#ifndef __noreturn
#	if __STDC_VERSION__ >= 201112L
#		if !IS_BIONIC
#			define __noreturn _Noreturn
#		else
#			define __noreturn __attribute__((__noreturn__))
#		endif
#	elif IS_BIONIC
#		define __noreturn __attribute__((__noreturn__))
#	else
#		define __noreturn __attribute__((noreturn))
#	endif
#endif

#ifndef __hot
#	define __hot __attribute__((hot))
#endif

#ifndef __unused
#	define __unused __attribute__((unused))
#endif

/*
 * __ro_after_init is used to mark things that are read-only after init (i.e.
 * after mark_rodata_ro() has been called). These are effectively read-only,
 * but may get written to during init, so can't live in .rodata (via "const").
 */
#ifndef __ro_after_init
#     define __ro_after_init __attribute__((__section__(".data..ro_after_init")))
#endif

#define __cgfsng_ops

#ifndef __returns_twice
#define __returns_twice __attribute__((returns_twice))
#endif

#ifndef __hidden
#define __hidden __attribute__((visibility("hidden")))
#endif

#endif /* __LXC_COMPILER_H */
