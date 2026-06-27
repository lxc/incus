#!/bin/bash
# Build per-architecture Incus client MSI installers.
set -eu

VERSION="${VERSION:?VERSION must be set}"
here="$(dirname "$0")"
out="installers"

mkdir -p "${out}"
bash "${here}/make-license-rtf.sh" COPYING "${out}/license.rtf"

build_one() {
    goarch="$1"
    wixarch="$2"
    name="$3"

    CGO_ENABLED=0 GOOS=windows GOARCH="${goarch}" go build -o "${out}/incus.exe" ./cmd/incus
    wix build -arch "${wixarch}" \
        -ext WixToolset.UI.wixext \
        -d Version="${VERSION}" \
        -d BinPath="${out}/incus.exe" \
        -d LicenseRtf="${out}/license.rtf" \
        -o "${out}/incus.windows.${name}.msi" \
        "${here}/incus.wxs"
}

build_one amd64 x64 x86_64
build_one arm64 arm64 aarch64

rm -f "${out}/incus.exe" "${out}/license.rtf"
