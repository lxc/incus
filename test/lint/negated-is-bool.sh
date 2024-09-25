#!/bin/sh -eu

echo "Checking usage of negated shared.Is(True|False)*() functions..."

OUT=$(git grep --untracked -P '!(shared\.)?Is(True|False).*\(' '*.go' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: negated shared.Is(True|False)*() function in script: ${OUT}"
  exit 1
fi
