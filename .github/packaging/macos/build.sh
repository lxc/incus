#!/bin/bash
# Build a universal Incus client PKG installer.
set -eu

VERSION="${VERSION:?VERSION must be set}"
here="$(dirname "$0")"
out="installers"
root="pkgroot"
res="pkgresources"

mkdir -p "${out}" "${root}/usr/local/bin" "${res}"
cp COPYING "${res}/LICENSE.txt"

CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o incus.amd64 ./cmd/incus
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o incus.arm64 ./cmd/incus
lipo -create -output "${root}/usr/local/bin/incus" incus.amd64 incus.arm64
chmod 0755 "${root}/usr/local/bin/incus"

pkgbuild --root "${root}" \
    --identifier org.linuxcontainers.incus \
    --version "${VERSION}" \
    --install-location / \
    incus-component.pkg

productbuild --distribution "${here}/distribution.xml" \
    --package-path . \
    --resources "${res}" \
    "${out}/incus.macos.pkg"

rm -rf incus.amd64 incus.arm64 incus-component.pkg "${root}" "${res}"
