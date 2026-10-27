#!/bin/sh -eu

echo "Checking for golangci-lint errors..."
golangci-lint run --timeout 5m
