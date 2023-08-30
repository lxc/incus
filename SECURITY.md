# Security policy

## Supported versions
<!-- Include start supported versions -->

Incus has two types of releases:

- Feature releases
- LTS releases

For feature releases, only the latest one is supported, and we usually
don't do point releases. Instead, users are expected to wait until the
next release.

For LTS releases, we do periodic bugfix releases that include an
accumulation of bugfixes from the feature releases. Such bugfix releases
do not include new features.

<!-- Include end supported versions -->

## What qualifies as a security issue

We don't consider privileged containers to be root safe, so any exploit
allowing someone to escape them will not qualify as a security issue.
This doesn't mean that we're not interested in preventing such escapes,
but we simply do not consider such containers to be root safe.

Unprivileged container escapes are certainly something we'd consider a
security issue, especially if somehow facilitated by Incus.

## Reporting security issues

Security issues can be reported by e-mail to security@linuxcontainers.org.
Alternatively security issues can also be reported through Github at: https://github.com/lxc/incus/security/advisories/new
