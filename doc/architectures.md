(architectures)=
# Architectures

Incus can run on just about any architecture that is supported by the Linux kernel and by Go.

Some entities in Incus are tied to an architecture, for example, the instances, instance snapshots and images.

The following table lists all supported architectures including their unique identifier and the name used to refer to them.
The architecture names are typically aligned with the Linux kernel architecture names.

ID    | Name          | Notes                           | Personalities
:---  | :---          | :----                           | :------------
1     | `i686`        | 32bit Intel x86                 |
2     | `x86_64`      | 64bit Intel x86                 | `x86`
3     | `armv7l`      | 32bit ARMv7 little-endian       |
4     | `aarch64`     | 64bit ARMv8 little-endian       | `armv7` (optional)
5     | `ppc`         | 32bit PowerPC big-endian        |
6     | `ppc64`       | 64bit PowerPC big-endian        | `powerpc`
7     | `ppc64le`     | 64bit PowerPC little-endian     |
8     | `s390x`       | 64bit ESA/390 big-endian        |
9     | `mips`        | 32bit MIPS                      |
10    | `mips64`      | 64bit MIPS                      | `mips`
11    | `riscv32`     | 32bit RISC-V little-endian      |
12    | `riscv64`     | 64bit RISC-V little-endian      |

```{note}
Incus cares only about the kernel architecture, not the particular userspace flavor as determined by the toolchain.

That means that Incus considers ARMv7 hard-float to be the same as ARMv7 soft-float and refers to both as `armv7`.
If useful to the user, the exact userspace ABI may be set as an image and container property, allowing easy query.
```
