#!/bin/sh -eu

echo "Checking for vulnerabilities in Go dependencies using govulncheck..."

GOVERSION=$(go version | cut -d' ' -f3 | sed "s/go//g")
cp go.mod go.mod.bak
sed "s/^go 1.*/go ${GOVERSION}/" -i go.mod
govulncheck ./...
mv go.mod.bak go.mod
