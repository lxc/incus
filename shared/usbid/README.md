This Go package is a variant of `usbid` from https://github.com/google/gousb.
It was written by the gousb maintainers and contributors and then adapted for our use.

Main changes:
 - Doesn't load on import (reduced memory footprint).
 - Uses system-local USB database (to always match `lsusb`).
 - Doesn't import anything outside of built-in Go packages.
 - Doesn't use or indirectly rely on CGO as this code is often used in cross-built static binaries.

Most users will want to stick to the upstream google/gousb version instead.
This fork is really meant for our special use case and may be further trimmed down in the future.
