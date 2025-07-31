#!/bin/sh -eu

echo "Checking for mixed tabs and spaces in shell scripts..."

OUT=$(git grep --untracked -lP '\t' '*.sh' || true)
if [ -n "${OUT}" ]; then
    echo "ERROR: mixed tabs and spaces in script: ${OUT}"
    exit 1
fi
