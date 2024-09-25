#!/bin/sh -eu

echo "Checking for short form imports..."

OUT=$(git grep --untracked -n -P '^\s*import\s+"' '*.go' ':!:test/mini-oidc/storage/*.go' | grep -v ':import "C"$' || true)
if [ -n "${OUT}" ]; then
  echo "ERROR: found short form imports: ${OUT}"
  exit 1
fi
