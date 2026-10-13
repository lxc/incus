# Build environment for Anthropic's OSS Scanner (https://github.com/anthropics/oss-scanner).
# The scanner builds this image online and then audits Incus inside it offline,
# so this file must fetch everything the build and the tests need.
FROM golang:1-trixie

ENV DEBIAN_FRONTEND=noninteractive \
    CGO_ENABLED=1 \
    GOTOOLCHAIN=local

# Build dependencies, plus the tools that incusd and its tests call at runtime.
RUN apt-get update && apt-get install -y --no-install-recommends \
      acl attr bind9-dnsutils ca-certificates curl gettext git iproute2 \
      libacl1-dev libcap-dev libcowsql-dev libdbus-1-dev libseccomp-dev \
      libselinux-dev libsqlite3-dev libudev-dev lxc-dev make nftables \
      pkg-config rsync squashfs-tools tar xz-utils zstd \
    && rm -rf /var/lib/apt/lists/*

# The scanner supplies the Incus checkout as the build context.
COPY . /src
WORKDIR /src

# Build as the maintainers do; the binaries land in /go/bin, which is on PATH.
RUN make

# Run the unit tests so their modules are cached, but keep the image if some fail.
RUN go test ./... || echo "WARNING: go test reported failures"

# New reproducers and patches must also build with the network disabled.
ENV GOPROXY=off \
    GOSUMDB=off
