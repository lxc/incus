#!/bin/sh -eu

echo "Checking for oneline assign & test..."

# Recursively grep go files for if statements that contain assignments.
! git grep --untracked -P -n '^\s+if.*:=.*;.*{\s*$' -- '*.go' ':!:test/mini-oidc/storage/*.go'
