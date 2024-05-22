package apparmor

import (
	"text/template"
)

var lxcProfileTpl = template.Must(template.New("lxcProfile").Parse(`#include <tunables/global>
profile "{{ .name }}" flags=(attach_disconnected,mediate_deleted) {
  ### Base profile
  capability,
  dbus,
  file,
  network,
  umount,

  # Extra binaries
{{- range $index, $element := .extra_binaries }}
{{ $element }}                               mrix,
{{- end }}

  # Hide common denials
  deny mount options=(ro,remount) -> /,
  deny mount options=(ro,remount,silent) -> /,

  # Allow normal signal handling
  signal (receive),
  signal peer=@{profile_name},

  # Allow normal process handling
  ptrace (readby),
  ptrace (tracedby),
  ptrace peer=@{profile_name},

  # Handle binfmt
  mount fstype=binfmt_misc -> /proc/sys/fs/binfmt_misc/,
{{- if not .kernel_binfmt }}
  deny /proc/sys/fs/binfmt_misc/{,**} rwklx,
{{- end }}

  # Handle cgroupfs
  mount options=(ro,nosuid,nodev,noexec,remount,strictatime) -> /sys/fs/cgroup/,

  # Handle configfs
  mount fstype=configfs -> /sys/kernel/config/,
  deny /sys/kernel/config/{,**} rwklx,

  # Handle debugfs
  mount fstype=debugfs -> /sys/kernel/debug/,
  deny /sys/kernel/debug/{,**} rwklx,

  # Handle efivarfs
  mount fstype=efivarfs -> /sys/firmware/efi/efivars/,
  deny /sys/firmware/efi/efivars/{,**} rwklx,

  # Handle tracefs
  mount fstype=tracefs -> /sys/kernel/tracing/,
  deny /sys/kernel/tracing/{,**} rwklx,

  # Handle fuse
  mount fstype=fuse,
  mount fstype=fuse.*,
  mount fstype=fusectl -> /sys/fs/fuse/connections/,

  # Handle hugetlbfs
  mount fstype=hugetlbfs,

  # Handle mqueue
  mount fstype=mqueue,

  # Handle proc
  mount fstype=proc -> /proc/,
  deny /proc/bus/** wklx,
  deny /proc/kcore rwklx,
  deny /proc/sysrq-trigger rwklx,
  deny /proc/acpi/** rwklx,

  # Handle securityfs (access handled separately)
  mount fstype=securityfs -> /sys/kernel/security/,

  # Handle sysfs (access handled below)
  mount fstype=sysfs -> /sys/,
  mount options=(rw,nosuid,nodev,noexec,remount) -> /sys/,

  # Handle /run remounts.
  mount options=(rw,nosuid,nodev,remount) -> /run/,

  # Handle ramfs (same as tmpfs)
  mount fstype=ramfs,

  # Handle tmpfs
  mount fstype=tmpfs,

  # Allow limited modification of mount propagation
  mount options=(rw,slave) -> /,
  mount options=(rw,rslave) -> /,
  mount options=(rw,shared) -> /,
  mount options=(rw,rshared) -> /,
  mount options=(rw,private) -> /,
  mount options=(rw,rprivate) -> /,
  mount options=(rw,unbindable) -> /,
  mount options=(rw,runbindable) -> /,

  # Allow various ro-bind-*re*-mounts of anything except /proc, /sys and /dev/.lxc
  mount options=(ro,remount,bind) /[^spd]*{,/**},
  mount options=(ro,remount,bind) /d[^e]*{,/**},
  mount options=(ro,remount,bind) /de[^v]*{,/**},
  mount options=(ro,remount,bind) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind) /dev?*{,/**},
  mount options=(ro,remount,bind) /p[^r]*{,/**},
  mount options=(ro,remount,bind) /pr[^o]*{,/**},
  mount options=(ro,remount,bind) /pro[^c]*{,/**},
  mount options=(ro,remount,bind) /proc?*{,/**},
  mount options=(ro,remount,bind) /s[^y]*{,/**},
  mount options=(ro,remount,bind) /sy[^s]*{,/**},
  mount options=(ro,remount,bind) /sys?*{,/**},

  mount options=(ro,remount,bind,nodev) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nodev) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nodev) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nodev) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nodev) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nodev) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nodev) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nodev) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nodev) /dev?*{,/**},
  mount options=(ro,remount,bind,nodev) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nodev) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nodev) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nodev) /proc?*{,/**},
  mount options=(ro,remount,bind,nodev) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nodev) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nodev) /sys?*{,/**},

  mount options=(ro,remount,bind,noexec) /[^spd]*{,/**},
  mount options=(ro,remount,bind,noexec) /d[^e]*{,/**},
  mount options=(ro,remount,bind,noexec) /de[^v]*{,/**},
  mount options=(ro,remount,bind,noexec) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,noexec) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,noexec) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,noexec) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,noexec) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,noexec) /dev?*{,/**},
  mount options=(ro,remount,bind,noexec) /p[^r]*{,/**},
  mount options=(ro,remount,bind,noexec) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,noexec) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,noexec) /proc?*{,/**},
  mount options=(ro,remount,bind,noexec) /s[^y]*{,/**},
  mount options=(ro,remount,bind,noexec) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,noexec) /sys?*{,/**},

  mount options=(ro,remount,bind,noexec,nodev) /[^spd]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /d[^e]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /de[^v]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /dev?*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /p[^r]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /proc?*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /s[^y]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,noexec,nodev) /sys?*{,/**},

  mount options=(ro,remount,bind,noatime) /[^spd]*{,/**},
  mount options=(ro,remount,bind,noatime) /d[^e]*{,/**},
  mount options=(ro,remount,bind,noatime) /de[^v]*{,/**},
  mount options=(ro,remount,bind,noatime) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,noatime) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,noatime) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,noatime) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,noatime) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,noatime) /dev?*{,/**},
  mount options=(ro,remount,bind,noatime) /p[^r]*{,/**},
  mount options=(ro,remount,bind,noatime) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,noatime) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,noatime) /proc?*{,/**},
  mount options=(ro,remount,bind,noatime) /s[^y]*{,/**},
  mount options=(ro,remount,bind,noatime) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,noatime) /sys?*{,/**},

  mount options=(ro,remount,bind,nosuid) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nosuid) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nosuid) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nosuid) /dev?*{,/**},
  mount options=(ro,remount,bind,nosuid) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nosuid) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nosuid) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid) /proc?*{,/**},
  mount options=(ro,remount,bind,nosuid) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nosuid) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nosuid) /sys?*{,/**},

  mount options=(ro,remount,bind,nosuid,nodev) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /dev?*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /proc?*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nosuid,nodev) /sys?*{,/**},

  mount options=(ro,remount,bind,nosuid,noexec) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /dev?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /proc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec) /sys?*{,/**},

  mount options=(ro,remount,bind,nosuid,noexec,nodev) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /dev?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /proc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,nodev) /sys?*{,/**},

  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /[^spd]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /d[^e]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /de[^v]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev/.[^l]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev/.l[^x]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev/.lx[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev/.lxc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev/[^.]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /dev?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /p[^r]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /pr[^o]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /pro[^c]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /proc?*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /s[^y]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /sy[^s]*{,/**},
  mount options=(ro,remount,bind,nosuid,noexec,strictatime) /sys?*{,/**},

  # Allow bind-mounts of anything except /proc, /sys and /dev/.lxc
  mount options=(rw,bind) /[^spd]*{,/**},
  mount options=(rw,bind) /d[^e]*{,/**},
  mount options=(rw,bind) /de[^v]*{,/**},
  mount options=(rw,bind) /dev/.[^l]*{,/**},
  mount options=(rw,bind) /dev/.l[^x]*{,/**},
  mount options=(rw,bind) /dev/.lx[^c]*{,/**},
  mount options=(rw,bind) /dev/.lxc?*{,/**},
  mount options=(rw,bind) /dev/[^.]*{,/**},
  mount options=(rw,bind) /dev?*{,/**},
  mount options=(rw,bind) /p[^r]*{,/**},
  mount options=(rw,bind) /pr[^o]*{,/**},
  mount options=(rw,bind) /pro[^c]*{,/**},
  mount options=(rw,bind) /proc?*{,/**},
  mount options=(rw,bind) /s[^y]*{,/**},
  mount options=(rw,bind) /sy[^s]*{,/**},
  mount options=(rw,bind) /sys?*{,/**},

  # Allow rbind-mounts of anything except /, /dev, /proc and /sys
  mount options=(rw,rbind) /[^spd]*{,/**},
  mount options=(rw,rbind) /d[^e]*{,/**},
  mount options=(rw,rbind) /de[^v]*{,/**},
  mount options=(rw,rbind) /dev?*{,/**},
  mount options=(rw,rbind) /p[^r]*{,/**},
  mount options=(rw,rbind) /pr[^o]*{,/**},
  mount options=(rw,rbind) /pro[^c]*{,/**},
  mount options=(rw,rbind) /proc?*{,/**},
  mount options=(rw,rbind) /s[^y]*{,/**},
  mount options=(rw,rbind) /sy[^s]*{,/**},
  mount options=(rw,rbind) /sys?*{,/**},

  # Allow moving mounts except for /proc, /sys and /dev/.lxc
  mount options=(rw,move) /[^spd]*{,/**},
  mount options=(rw,move) /d[^e]*{,/**},
  mount options=(rw,move) /de[^v]*{,/**},
  mount options=(rw,move) /dev/.[^l]*{,/**},
  mount options=(rw,move) /dev/.l[^x]*{,/**},
  mount options=(rw,move) /dev/.lx[^c]*{,/**},
  mount options=(rw,move) /dev/.lxc?*{,/**},
  mount options=(rw,move) /dev/[^.]*{,/**},
  mount options=(rw,move) /dev?*{,/**},
  mount options=(rw,move) /p[^r]*{,/**},
  mount options=(rw,move) /pr[^o]*{,/**},
  mount options=(rw,move) /pro[^c]*{,/**},
  mount options=(rw,move) /proc?*{,/**},
  mount options=(rw,move) /s[^y]*{,/**},
  mount options=(rw,move) /sy[^s]*{,/**},
  mount options=(rw,move) /sys?*{,/**},

  # Block dangerous paths under /proc/sys
  deny /proc/sys/[^fkn]*{,/**} wklx,
  deny /proc/sys/f[^s]*{,/**} wklx,
  deny /proc/sys/fs/[^b]*{,/**} wklx,
  deny /proc/sys/fs/b[^i]*{,/**} wklx,
  deny /proc/sys/fs/bi[^n]*{,/**} wklx,
  deny /proc/sys/fs/bin[^f]*{,/**} wklx,
  deny /proc/sys/fs/binf[^m]*{,/**} wklx,
  deny /proc/sys/fs/binfm[^t]*{,/**} wklx,
  deny /proc/sys/fs/binfmt[^_]*{,/**} wklx,
  deny /proc/sys/fs/binfmt_[^m]*{,/**} wklx,
  deny /proc/sys/fs/binfmt_m[^i]*{,/**} wklx,
  deny /proc/sys/fs/binfmt_mi[^s]*{,/**} wklx,
  deny /proc/sys/fs/binfmt_mis[^c]*{,/**} wklx,
  deny /proc/sys/fs/binfmt_misc?*{,/**} wklx,
  deny /proc/sys/fs?*{,/**} wklx,
  deny /proc/sys/k[^e]*{,/**} wklx,
  deny /proc/sys/ke[^r]*{,/**} wklx,
  deny /proc/sys/ker[^n]*{,/**} wklx,
  deny /proc/sys/kern[^e]*{,/**} wklx,
  deny /proc/sys/kerne[^l]*{,/**} wklx,
  deny /proc/sys/kernel/[^smhd]*{,/**} wklx,
  deny /proc/sys/kernel/d[^o]*{,/**} wklx,
  deny /proc/sys/kernel/do[^m]*{,/**} wklx,
  deny /proc/sys/kernel/dom[^a]*{,/**} wklx,
  deny /proc/sys/kernel/doma[^i]*{,/**} wklx,
  deny /proc/sys/kernel/domai[^n]*{,/**} wklx,
  deny /proc/sys/kernel/domain[^n]*{,/**} wklx,
  deny /proc/sys/kernel/domainn[^a]*{,/**} wklx,
  deny /proc/sys/kernel/domainna[^m]*{,/**} wklx,
  deny /proc/sys/kernel/domainnam[^e]*{,/**} wklx,
  deny /proc/sys/kernel/domainname?*{,/**} wklx,
  deny /proc/sys/kernel/h[^o]*{,/**} wklx,
  deny /proc/sys/kernel/ho[^s]*{,/**} wklx,
  deny /proc/sys/kernel/hos[^t]*{,/**} wklx,
  deny /proc/sys/kernel/host[^n]*{,/**} wklx,
  deny /proc/sys/kernel/hostn[^a]*{,/**} wklx,
  deny /proc/sys/kernel/hostna[^m]*{,/**} wklx,
  deny /proc/sys/kernel/hostnam[^e]*{,/**} wklx,
  deny /proc/sys/kernel/hostname?*{,/**} wklx,
  deny /proc/sys/kernel/m[^s]*{,/**} wklx,
  deny /proc/sys/kernel/ms[^g]*{,/**} wklx,
  deny /proc/sys/kernel/msg*/** wklx,
  deny /proc/sys/kernel/s[^he]*{,/**} wklx,
  deny /proc/sys/kernel/se[^m]*{,/**} wklx,
  deny /proc/sys/kernel/sem*/** wklx,
  deny /proc/sys/kernel/sh[^m]*{,/**} wklx,
  deny /proc/sys/kernel/shm*/** wklx,
  deny /proc/sys/kernel?*{,/**} wklx,
  deny /proc/sys/n[^e]*{,/**} wklx,
  deny /proc/sys/ne[^t]*{,/**} wklx,
  deny /proc/sys/net?*{,/**} wklx,

  # Block dangerous paths under /sys
  deny /sys/[^fdck]*{,/**} wklx,
  deny /sys/c[^l]*{,/**} wklx,
  deny /sys/cl[^a]*{,/**} wklx,
  deny /sys/cla[^s]*{,/**} wklx,
  deny /sys/clas[^s]*{,/**} wklx,
  deny /sys/class/[^n]*{,/**} wklx,
  deny /sys/class/n[^e]*{,/**} wklx,
  deny /sys/class/ne[^t]*{,/**} wklx,
  deny /sys/class/net?*{,/**} wklx,
  deny /sys/class?*{,/**} wklx,
  deny /sys/d[^e]*{,/**} wklx,
  deny /sys/de[^v]*{,/**} wklx,
  deny /sys/dev[^i]*{,/**} wklx,
  deny /sys/devi[^c]*{,/**} wklx,
  deny /sys/devic[^e]*{,/**} wklx,
  deny /sys/device[^s]*{,/**} wklx,
  deny /sys/devices/[^v]*{,/**} wklx,
  deny /sys/devices/v[^i]*{,/**} wklx,
  deny /sys/devices/vi[^r]*{,/**} wklx,
  deny /sys/devices/vir[^t]*{,/**} wklx,
  deny /sys/devices/virt[^u]*{,/**} wklx,
  deny /sys/devices/virtu[^a]*{,/**} wklx,
  deny /sys/devices/virtua[^l]*{,/**} wklx,
  deny /sys/devices/virtual/[^n]*{,/**} wklx,
  deny /sys/devices/virtual/n[^e]*{,/**} wklx,
  deny /sys/devices/virtual/ne[^t]*{,/**} wklx,
  deny /sys/devices/virtual/net?*{,/**} wklx,
  deny /sys/devices/virtual?*{,/**} wklx,
  deny /sys/devices?*{,/**} wklx,
  deny /sys/f[^s]*{,/**} wklx,
  deny /sys/fs/[^bc]*{,/**} wklx,
  deny /sys/fs/b[^p]*{,/**} wklx,
  deny /sys/fs/bp[^f]*{,/**} wklx,
  deny /sys/fs/bpf?*{,/**} wklx,
  deny /sys/fs/c[^g]*{,/**} wklx,
  deny /sys/fs/cg[^r]*{,/**} wklx,
  deny /sys/fs/cgr[^o]*{,/**} wklx,
  deny /sys/fs/cgro[^u]*{,/**} wklx,
  deny /sys/fs/cgrou[^p]*{,/**} wklx,
  deny /sys/fs/cgroup?*{,/**} wklx,
  deny /sys/fs?*{,/**} wklx,

{{- if .feature_unix }}

  ### Feature: unix
  # Allow receive via unix sockets from anywhere
  unix (receive),

  # Allow all unix in the container
  unix peer=(label=@{profile_name}),
{{- end }}

{{- if .feature_cgns }}

  ### Feature: cgroup namespace
  mount fstype=cgroup -> /sys/fs/cgroup/**,
{{- if .feature_cgroup2 }}
  mount fstype=cgroup2 -> /sys/fs/cgroup/**,
{{- end }}
{{- end }}

{{- if .feature_stacking }}

  ### Feature: apparmor stacking
  deny /sys/k[^e]*{,/**} wklx,
  deny /sys/ke[^r]*{,/**} wklx,
  deny /sys/ker[^n]*{,/**} wklx,
  deny /sys/kern[^e]*{,/**} wklx,
  deny /sys/kerne[^l]*{,/**} wklx,
  deny /sys/kernel/[^s]*{,/**} wklx,
  deny /sys/kernel/s[^e]*{,/**} wklx,
  deny /sys/kernel/se[^c]*{,/**} wklx,
  deny /sys/kernel/sec[^u]*{,/**} wklx,
  deny /sys/kernel/secu[^r]*{,/**} wklx,
  deny /sys/kernel/secur[^i]*{,/**} wklx,
  deny /sys/kernel/securi[^t]*{,/**} wklx,
  deny /sys/kernel/securit[^y]*{,/**} wklx,
  deny /sys/kernel/security/[^a]*{,/**} wklx,
  deny /sys/kernel/security/a[^p]*{,/**} wklx,
  deny /sys/kernel/security/ap[^p]*{,/**} wklx,
  deny /sys/kernel/security/app[^a]*{,/**} wklx,
  deny /sys/kernel/security/appa[^r]*{,/**} wklx,
  deny /sys/kernel/security/appar[^m]*{,/**} wklx,
  deny /sys/kernel/security/apparm[^o]*{,/**} wklx,
  deny /sys/kernel/security/apparmo[^r]*{,/**} wklx,
  deny /sys/kernel/security/apparmor?*{,/**} wklx,
  deny /sys/kernel/security?*{,/**} wklx,
  deny /sys/kernel?*{,/**} wklx,

  change_profile -> ":{{ .namespace }}:*",
  change_profile -> ":{{ .namespace }}://*",
{{- else }}

  ### Feature: apparmor stacking (not present)
  deny /sys/k*{,/**} wklx,
{{- end }}

{{- if .nesting }}

  ### Configuration: nesting
  pivot_root,

  # Allow sending signals and tracing children namespaces
  ptrace,
  signal,

  # Prevent access to hidden proc/sys mounts
  deny /dev/.lxc/proc/** rw,
  deny /dev/.lxc/sys/** rw,

  # Allow mounting proc and sysfs in the container
  mount fstype=proc -> /usr/lib/*/lxc/**,
  mount fstype=sysfs -> /usr/lib/*/lxc/**,

  # Allow nested Incus
  mount none -> /var/lib/incus/shmounts/,
  mount /var/lib/incus/shmounts/ -> /var/lib/incus/shmounts/,
  mount options=bind /var/lib/incus/shmounts/** -> /var/lib/incus/**,

  # FIXME: There doesn't seem to be a way to ask for:
  # mount options=(ro,nosuid,nodev,noexec,remount,bind),
  # as we always get mount to $cdir/proc/sys with those flags denied
  # So allow all mounts until that is straightened out:
  mount,

{{- if not .feature_stacking }}
  change_profile -> "{{ .name }}",
{{- end }}
{{- end }}

{{- if .unprivileged }}

  ### Configuration: unprivileged containers
  pivot_root,

  # Allow modifying mount propagation
  mount options=(rw,slave) -> /**,
  mount options=(rw,rslave) -> /**,
  mount options=(rw,shared) -> /**,
  mount options=(rw,rshared) -> /**,
  mount options=(rw,private) -> /**,
  mount options=(rw,rprivate) -> /**,
  mount options=(rw,unbindable) -> /**,
  mount options=(rw,runbindable) -> /**,

  # Allow all bind-mounts.
  mount options=(rw,bind) -> /**,
  mount options=(rw,rbind) -> /**,

  # Allow all move-mounts.
  mount options=(rw,move) -> /**,

  # Allow common combinations of bind/remount
  # NOTE: AppArmor bug effectively turns those into wildcards mount allow
  mount options=(ro,remount,bind),
  mount options=(ro,remount,bind,nodev),
  mount options=(ro,remount,bind,nodev,nosuid),
  mount options=(ro,remount,bind,noexec),
  mount options=(ro,remount,bind,noexec,nodev),
  mount options=(ro,remount,bind,nosuid),
  mount options=(ro,remount,bind,nosuid,nodev),
  mount options=(ro,remount,bind,nosuid,noexec),
  mount options=(ro,remount,bind,nosuid,noexec,nodev),
  mount options=(ro,remount,bind,noatime),
  mount options=(ro,remount,bind,noatime,nodev),
  mount options=(ro,remount,bind,noatime,noexec),
  mount options=(ro,remount,bind,noatime,nosuid),
  mount options=(ro,remount,bind,noatime,noexec,nodev),
  mount options=(ro,remount,bind,noatime,nosuid,nodev),
  mount options=(ro,remount,bind,noatime,nosuid,noexec),
  mount options=(ro,remount,bind,noatime,nosuid,noexec,nodev),
  mount options=(ro,remount,bind,nosuid,noexec,strictatime),
  mount options=(ro,remount,nosuid,noexec,strictatime),
{{- if .feature_nosymfollow }}
  mount options=(ro,remount,bind,nosuid,noexec,nodev,nosymfollow),
{{- end }}

  # Allow remounting things read-only
  mount options=(ro,remount) /,
  mount options=(ro,remount) /**,
{{- end }}

{{- if .raw }}

  ### Configuration: raw.apparmor
{{ .raw }}
{{- end }}
}
`))
