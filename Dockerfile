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
# UID 1000 = unprivileged app user; nginx reaches the app over the compose
# internal network (TCP), so no shared socket group is needed anymore.
RUN adduser -u 1000 -S app
COPY --from=build /out/onionshop /usr/local/bin/onionshop
USER 1000:1000
# Read-only rootfs: everything writable is a tmpfs declared in compose (/tmp).
# Logs go to stdout; the logging driver keeps them on tmpfs.
# Loopback TCP inside the container; reachable by nginx via the service name.
ENV ONIONSHOP_ADDR=127.0.0.1:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget --quiet --tries=1 -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/onionshop"]
CMD ["-addr", "127.0.0.1:8080"]
