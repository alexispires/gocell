# Binder image (https://mybinder.org): a Jupyter environment with the gocell kernel
# pre-installed, built on jupyter/docker-stacks' minimal-notebook, which already satisfies
# Binder's Dockerfile requirements (a non-root user at UID 1000, Jupyter preinstalled,
# launched via the standard docker-stacks entrypoint) -- so the only thing this file adds is
# the Go toolchain, a C linker (Go's plugin buildmode needs external linking on Linux, even
# for cgo-free code), and the compiled kernel itself.
FROM jupyter/minimal-notebook:latest

USER root

# build-essential provides the C linker plugin buildmode needs at both image-build time (for
# gocell-kernel/gocell-install themselves) and at every cell's compile time afterward.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates build-essential \
    && rm -rf /var/lib/apt/lists/*

# Keep in sync with the `go` directive in go.mod -- the plugin package requires the host
# kernel and every cell plugin to be built with the same toolchain.
ARG GO_VERSION=1.25.0
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/home/jovyan/go"
ENV PATH="${GOPATH}/bin:${PATH}"

USER jovyan

COPY --chown=jovyan:users . /home/jovyan/gocell-src
WORKDIR /home/jovyan/gocell-src

# Building here, rather than leaving it to the first cell, does two things: registers the
# kernelspec so it shows up in the launcher immediately, and warms the module cache (zmq4,
# x/tools, ...) so that later, at runtime, each cell's own plugin compile -- which resolves
# this same module via the `replace` directive in its generated go.mod (see
# pkg/compiler/builder.go) -- never needs network access, only Binder's already-limited egress.
RUN mkdir -p "${GOPATH}/bin" \
    && go build -o "${GOPATH}/bin/gocell-kernel" ./cmd/gocell-kernel \
    && go build -o "${GOPATH}/bin/gocell-install" ./cmd/gocell-install \
    && "${GOPATH}/bin/gocell-install"

# The example notebooks are what the Binder badge actually launches into -- keep them at the
# same repo-relative path (examples/...) a visitor would see on GitHub, directly under $HOME
# rather than buried in gocell-src, matching how docker-stacks images expect a user's working
# directory to look.
RUN cp -r examples /home/jovyan/examples

WORKDIR /home/jovyan
