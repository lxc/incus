#ifndef __INCUS_SYSCALL_NUMBERS_H
#define __INCUS_SYSCALL_NUMBERS_H

#ifndef _GNU_SOURCE
#define _GNU_SOURCE 1
#endif
#include <asm/unistd.h>
#include <errno.h>
#include <linux/keyctl.h>
#include <sched.h>
#include <stdint.h>
#include <sys/syscall.h>
#include <sys/types.h>
#include <unistd.h>

#ifndef __NR_pidfd_open
	#if defined __alpha__
		#define __NR_pidfd_open 544
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_open 4434
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_open 6434
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_open 5434
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_open (434 + 1024)
	#else
		#define __NR_pidfd_open 434
	#endif
#endif

#ifndef __NR_pidfd_getfd
	#if defined __alpha__
		#define __NR_pidfd_getfd 548
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_getfd 4438
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_getfd 6438
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_getfd 5438
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_getfd (438 + 1024)
	#else
		#define __NR_pidfd_getfd 438
	#endif
#endif

#ifndef __NR_pidfd_send_signal
	#if defined __alpha__
		#define __NR_pidfd_send_signal 534
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_pidfd_send_signal 4424
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_pidfd_send_signal 6424
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_pidfd_send_signal 5424
		#endif
	#elif defined __ia64__
		#define __NR_pidfd_send_signal (424 + 1024)
	#else
		#define __NR_pidfd_send_signal 424
	#endif
#endif

#ifndef __NR_clone3
	#if defined __alpha__
		#define __NR_clone3 545
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_clone3 4435
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_clone3 6435
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_clone3 5435
		#endif
	#elif defined __ia64__
		#define __NR_clone3 (435 + 1024)
	#else
		#define __NR_clone3 435
	#endif
#endif

#ifndef __NR_bpf
	#if defined __i386__
		#define __NR_bpf 357
	#elif defined __x86_64__
		#define __NR_bpf 321
	#elif defined __arm__
		#define __NR_bpf 386
	#elif defined __aarch64__
		#define __NR_bpf 386
	#elif defined __s390__
		#define __NR_bpf 351
	#elif defined __powerpc__
		#define __NR_bpf 361
	#elif defined __riscv
		#define __NR_bpf 280
	#elif defined __sparc__
		#define __NR_bpf 349
	#elif defined __ia64__
		#define __NR_bpf (317 + 1024)
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_bpf 4355
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_bpf 6319
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_bpf 5315
		#endif
	#elif defined __loongarch64
		#define __NR_bpf 280
	#else
		#define -1
		#warning "__NR_bpf not defined for your architecture"
	#endif
#endif

#ifndef __NR_close_range
	#if defined __alpha__
		#define __NR_close_range 546
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_close_range 4436
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_close_range 6436
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_close_range 5436
		#endif
	#elif defined __ia64__
		#define __NR_close_range (436 + 1024)
	#else
		#define __NR_close_range 436
	#endif
#endif

#ifndef __NR_open_tree
	#if defined __alpha__
		#define __NR_open_tree 538
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_open_tree 4428
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_open_tree 6428
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_open_tree 5428
		#endif
	#elif defined __ia64__
		#define __NR_open_tree (428 + 1024)
	#else
		#define __NR_open_tree 428
	#endif
#endif

#ifndef __NR_mount_setattr
	#if defined __alpha__
		#define __NR_mount_setattr 552
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_mount_setattr (442 + 4000)
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_mount_setattr (442 + 6000)
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_mount_setattr (442 + 5000)
		#endif
	#elif defined __ia64__
		#define __NR_mount_setattr (442 + 1024)
	#else
		#define __NR_mount_setattr 442
	#endif
#endif

#ifndef __NR_move_mount
	#if defined __alpha__
		#define __NR_move_mount 539
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_move_mount 4429
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_move_mount 6429
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_move_mount 5429
		#endif
	#elif defined __ia64__
		#define __NR_move_mount (428 + 1024)
	#else
		#define __NR_move_mount 429
	#endif
#endif

#ifndef __NR_kcmp
	#if defined __i386__
		#define __NR_kcmp 349
	#elif defined __x86_64__
		#define __NR_kcmp 312
	#elif defined __arm__
		#define __NR_kcmp 378
	#elif defined __aarch64__
		#define __NR_kcmp 378
	#elif defined __s390__
		#define __NR_kcmp 343
	#elif defined __powerpc__
		#define __NR_kcmp 354
	#elif defined __riscv
		#define __NR_kcmp 272
	#elif defined __sparc__
		#define __NR_kcmp 341
	#elif defined __ia64__
		#define __NR_kcmp (321 + 1024)
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_kcmp (347 + 4000)
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_kcmp (311 + 6000)
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_kcmp (306 + 5000)
		#endif
	#elif defined __loongarch64
		#define __NR_kcmp 272
	#else
		#define -1
		#warning "__NR_kcmp not defined for your architecture"
	#endif
#endif

#ifndef __NR_fsopen
	#if defined __alpha__
		#define __NR_fsopen 540
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_fsopen 4430
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_fsopen 6430
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_fsopen 5430
		#endif
	#elif defined __ia64__
		#define __NR_fsopen (430 + 1024)
	#else
		#define __NR_fsopen 430
	#endif
#endif

#ifndef __NR_fsconfig
	#if defined __alpha__
		#define __NR_fsconfig 541
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_fsconfig 4431
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_fsconfig 6431
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_fsconfig 5431
		#endif
	#elif defined __ia64__
		#define __NR_fsconfig (431 + 1024)
	#else
		#define __NR_fsconfig 431
	#endif
#endif

#ifndef __NR_fsmount
	#if defined __alpha__
		#define __NR_fsmount 542
	#elif defined _MIPS_SIM
		#if _MIPS_SIM == _MIPS_SIM_ABI32	/* o32 */
			#define __NR_fsmount 4432
		#endif
		#if _MIPS_SIM == _MIPS_SIM_NABI32	/* n32 */
			#define __NR_fsmount 6432
		#endif
		#if _MIPS_SIM == _MIPS_SIM_ABI64	/* n64 */
			#define __NR_fsmount 5432
		#endif
	#elif defined __ia64__
		#define __NR_fsmount (432 + 1024)
	#else
		#define __NR_fsmount 432
	#endif
#endif

#endif /* __INCUS_SYSCALL_NUMBERS_H */
