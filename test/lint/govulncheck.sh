#!/bin/sh -eu

echo "Checking for vulnerabilities in Go dependencies using govulncheck..."

govulncheck ./...
