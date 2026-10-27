#!/bin/bash
# Wrap a plain-text license into a minimal RTF for the WiX license dialog.
set -eu

src="$1"
dst="$2"

{
    printf '{\\rtf1\\ansi\\deff0{\\fonttbl{\\f0 Courier New;}}\\fs16\n'
    sed -e 's/\\/\\\\/g' -e 's/{/\\{/g' -e 's/}/\\}/g' -e 's/$/\\par/' "$src"
    printf '}\n'
} > "$dst"
