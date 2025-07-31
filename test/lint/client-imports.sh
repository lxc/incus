#!/bin/sh -eu

echo "Checking for imports that have been added to client or shared..."

OUT=$(go list -f '{{ join .Imports "\n" }}' ./client ./shared/api ./shared/cliconfig | grep -F . | sort -u | diff -u test/godeps.list - || true)
if [ -n "${OUT}" ]; then
    echo "ERROR: you added a new dependency to the client or shared; please make sure this is what you want"
    echo "${OUT}"
    exit 1
fi
