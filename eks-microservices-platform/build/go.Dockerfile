# Multi-stage build for the Go services.
#
# Built once and parameterised by SERVICE, so gateway and catalog cannot drift
# apart in base image, build flags or hardening.

# --- Build stage -------------------------------------------------------------
FROM golang:1.25-alpine AS build

# Digest-pinning the base image would be stricter still; the CI workflow
# resolves and records digests in the SBOM instead, so a rebuild is traceable
# without editing this file on every upstream patch release.
WORKDIR /src

# Dependencies are copied and downloaded before the source, so an edit to any
# .go file reuses the cached module layer instead of re-downloading the whole
# dependency graph on every build.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG SERVICE
ARG VERSION=dev
ARG TARGETARCH

# CGO_ENABLED=0 produces a static binary, which is what allows the runtime stage
# to be `scratch` with no libc at all.
#
# -trimpath removes local filesystem paths from the binary, so the image does
# not leak the build machine's directory layout, and the build is reproducible
# across machines.
#
# -s -w strips the symbol table and DWARF data; roughly a third off the binary
# for a service that is debugged from logs, metrics and traces rather than by
# attaching a debugger to a production pod.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/app \
      ./cmd/${SERVICE}

# --- Runtime stage -----------------------------------------------------------
#
# scratch: no shell, no package manager, no busybox, no libc. An attacker who
# achieves code execution finds nothing to pivot with — no `sh` to spawn, no
# `curl` to exfiltrate with, no `apk` to install one. It also means the image
# has no CVEs of its own, because it contains nothing but the binary.
FROM scratch

# Two files are still required. CA certificates, or every outbound TLS
# connection fails with an unhelpful x509 error. And zoneinfo, or time.LoadLocation
# fails at runtime in a way that only shows up on the code path that uses it.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

COPY --from=build /out/app /app

# Numeric, not a name: scratch has no /etc/passwd, so a username cannot be
# resolved and the container would fail to start. This matches runAsUser in the
# chart, and pairs with runAsNonRoot so the kubelet can verify it is not 0.
USER 10001:10001

EXPOSE 8080 9090

# Exec form, so the binary is PID 1 and receives SIGTERM directly. The shell
# form would put /bin/sh at PID 1 — which scratch does not have — and, where it
# does exist, the shell does not forward signals, so graceful shutdown never
# runs and every rolling update drops connections.
ENTRYPOINT ["/app"]
