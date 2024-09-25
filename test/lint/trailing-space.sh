#!/bin/sh -eu

echo "Checking that there are no trailing spaces in shell scripts..."

OUT=$(git grep --untracked -lP "\s$" '*.sh' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: trailing space in script: ${OUT}"
  exit 1
fi
