ARG GO_VERSION=1.23
ARG DEBIAN_VERSION=bookworm

# Go development container
FROM golang:${GO_VERSION}-${DEBIAN_VERSION}
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000

# Install necessary tools.
RUN sed -r -i 's/^Components: main$/Components: main contrib/g' /etc/apt/sources.list.d/debian.sources && \
    apt update && \
    apt install -y \
      acl \
      aspell \
      aspell-en \
      attr \
      autoconf \
      automake \
      bind9-dnsutils \
      btrfs-progs \
      busybox-static \
      ceph-common \
      curl \
      dnsmasq-base \
      ebtables \
      flake8 \
      gettext \
      git \
      jq \
      less \
      libacl1-dev \
      libcap-dev \
      # libcowsql-dev
      libdbus-1-dev \
      # liblxc-dev \
      liblxc1 \
      liblz4-dev \
      libseccomp-dev \
      libselinux1-dev \
      libsqlite3-dev \
      libtool \
      libudev-dev \
      libusb-1.0-0-dev \
      libuv1-dev \
      locales \
      locales-all \
      lvm2 \
      lxc-dev \
      lxc-templates \
      make \
      man-db \
      pipx \
      pkg-config \
      protoc-gen-go \
      python3-matplotlib \
      python3.11-venv \
      rsync \
      ruby-mdl \
      shellcheck \
      socat \
      sqlite3 \
      squashfs-tools \
      sudo \
      tar \
      tcl \
      thin-provisioning-tools \
      vim \
      # Disabled for now, very slow to install.
      # zfsutils-linux
      xz-utils

# With pipx >= 1.5.0, we could use pipx --global instead.
RUN PIPX_HOME=/opt/pipx PIPX_BIN_DIR=/usr/local/bin pipx install codespell

# Add vscode user and add it to sudoers.
RUN groupadd -g 1000 $USERNAME && \
    useradd -s /bin/bash -u $USER_UID -g $USER_GID -m $USERNAME && \
    mkdir -p /etc/sudoers.d && \
    echo $USERNAME ALL=\(root\) NOPASSWD:ALL > /etc/sudoers.d/$USERNAME && \
    chmod 0440 /etc/sudoers.d/$USERNAME

# Setup for vscode user.
USER $USERNAME
ENV EDITOR=vi \
    LANG=en_US.UTF-8 \
    CGO_CFLAGS="-I/home/vscode/vendor/raft/include/ -I/home/vscode/vendor/cowsql/include/" \
    CGO_LDFLAGS="-L/home/vscode/vendor/raft/.libs -L/home/vscode/vendor/cowsql/.libs/" \
    LD_LIBRARY_PATH="/home/vscode/vendor/raft/.libs/:/home/vscode/vendor/cowsql/.libs/" \
    CGO_LDFLAGS_ALLOW="(-Wl,-wrap,pthread_create)|(-Wl,-z,now)"

# Build Go tools with user vscode to ensure correct file and directory permissions for the build artifacts.
RUN go install -v github.com/google/go-licenses@latest && \
    go install -v github.com/766b/go-outliner@latest && \
    GOTOOLCHAIN="" go install -v golang.org/x/tools/gopls@latest && \
    go install -v github.com/go-delve/delve/cmd/dlv@latest && \
    go install -v golang.org/x/tools/cmd/goimports@latest && \
    go install -v golang.org/x/vuln/cmd/govulncheck@latest && \
    go install -v mvdan.cc/gofumpt@latest && \
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Make dependencies
COPY Makefile /home/vscode
RUN cd /home/vscode && \
    mkdir /home/vscode/vendor && \
    make deps
USER root

# Since we use a volume for /go to persist the content between executions, we need to preserve the binaries.
RUN mv /go/bin/* /usr/local/bin
