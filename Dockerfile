# syntax=docker/dockerfile:1
# OnionShop app image — hardened, non-root, read-only-rootfs compatible.
# Build context is the repo root; docker-compose.yml references `dockerfile: Dockerfile`.

# ---- Build stage (pinned toolchain matching go.mod) ----
FROM golang:1.22-alpine AS build
WORKDIR /src
# Copy the module manifest first so dependency download is layer-cached.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY db/ ./db/
COPY web/ ./web/
# Static, stripped build. CGO off => pure Go, no libc surprises at runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/onionshop ./cmd/onionshop

# ---- Runtime stage ----
FROM alpine:3.19
RUN adduser -u 1000 -S app
COPY --from=build /out/onionshop /usr/local/bin/onionshop
USER 1000:1000
# Read-only rootfs: everything writable is a tmpfs declared in compose (/tmp).
# Logs go to stdout; the logging driver keeps them on RAM.
#
# Bind policy: the binary enforces localhost-only unless ONIONSHOP_NET_NEVER=1
# declares a contained network namespace with no off-box route (docker-compose
# sets both vars there). The image default stays strict loopback so a bare
# `docker run` WITHOUT the marker still cannot expose the app on eth0: the
# process refuses to start rather than silently binding publicly.
ENV ONIONSHOP_ADDR=127.0.0.1:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget --quiet --tries=1 -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/onionshop"]
# No fixed -addr here: honor $ONIONSHOP_ADDR (compose overrides it together
# with ONIONSHOP_NET_NEVER for the contained-namespace bind).
CMD []
