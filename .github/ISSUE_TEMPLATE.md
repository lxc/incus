<!--
Github issues are used for bug reports. For support questions, please use [our forum](https://discuss.linuxcontainers.org/).

Please fill the template below as it will greatly help us track down your issue and reproduce it on our side.
Feel free to remove anything which doesn't apply to you and add more information where it makes sense.
-->

# Required information

 * Distribution:
 * Distribution version:
 * The output of "inc info" or if that fails:
   * Kernel version:
   * LXC version:
   * Incus version:
   * Storage backend in use:

# Issue description

A brief description of the problem. Should include what you were
attempting to do, what you did, what happened and what you expected to
see happen.

# Steps to reproduce

 1. Step one
 2. Step two
 3. Step three

# Information to attach

 - [ ] Any relevant kernel output (`dmesg`)
 - [ ] Container log (`inc info NAME --show-log`)
 - [ ] Container configuration (`inc config show NAME --expanded`)
 - [ ] Main daemon log (at /var/log/incus/incus.log)
 - [ ] Output of the client with --debug
 - [ ] Output of the daemon with --debug (alternatively output of `inc monitor` while reproducing the issue)
