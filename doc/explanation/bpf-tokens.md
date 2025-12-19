(bpf-tokens)=
# BPF token delegation

Incus supports delegating BPF capabilities via [BPF tokens](https://docs.ebpf.io/linux/concepts/token/), introduced in Linux kernel 6.9.

If any of the instance options {config:option}`instance-security:security.bpffs.delegate_cmds`,
{config:option}`instance-security:security.bpffs.delegate_maps`,
{config:option}`instance-security:security.bpffs.delegate_progs` or
{config:option}`instance-security:security.bpffs.delegate_attachs` is set, Incus mounts a BPF file system into the
container at the path specified by the {config:option}`instance-security:security.bpffs.path` option and delegates the
configured capabilities to it.

The permissible values for these options depend on the kernel version and can be found in `enums` in the BPF header file
(`include/uapi/linux/bpf.h` in the kernel tree, `/usr/include/linux/bpf.h` on most distributions if you have the kernel
sources installed):

| Key                               | Kernel `enum`      | Remove prefix |
| :---                              | :---               | :--- |
| `security.bpffs.delegate_cmds`    | `bpf_cmd`          | `BPF_` |
| `security.bpffs.delegate_maps`    | `bpf_map_type`     | `BPF_MAP_TYPE_` |
| `security.bpffs.delegate_progs`   | `bpf_prog_type`    | `BPF_PROG_TYPE_` |
| `security.bpffs.delegate_attachs` | `bpf_attach_type`  | `BPF_` |

Each of these options takes a comma-separated list of values, additionally the value `any` is supported to delegate all
possible values of the type.

## Example

| Key                               | Value |
| :---                              | :--- |
| `security.bpffs.delegate_cmds`    | `map_create,obj_get,link_create` |
| `security.bpffs.delegate_maps`    | `hash,array,devmap,queue,stack` |
| `security.bpffs.delegate_progs`   | `socket_filter,kprobe,cgroup_sysctl` |
| `security.bpffs.delegate_attachs` | `any` |

```bash
$ mount -t bpf
none on /sys/fs/bpf type bpf (rw,relatime,delegate_cmds=map_create:obj_get:link_create,delegate_maps=hash:array:devmap:queue:stack,delegate_progs=socket_filter:kprobe:cgroup_sysctl,delegate_attachs=any)
```
