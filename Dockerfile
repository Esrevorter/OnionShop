# syntax=docker/dockerfile:1
# OnionShop app image — hardened, non-root, read-only-rootfs compatible.
# Build context is the repo root; docker-compose.yml references `dockerfile: Dockerfile`.

# ---- Build stage (pinned toolchain matching go.mod) ----
FROM golang:1.19-alpine AS build
WORKDIR /src
# Module has no external dependencies today; copy sources only.
COPY go.mod ./
COPY cmd/ ./cmd/
COPY db/ ./db/
# Static, stripped build. CGO off => pure Go, no libc surprises at runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/onionshop ./cmd/onionshop

# ---- Runtime stage ----
FROM alpine:3.19
# UID 1000 = app user; GID 101 matches the nginx container's 'nginx' group so
# both can reach the shared UNIX socket (mode 0770, owner app:nginx).
RUN addgroup -g 101 -S appsock \
 && adduser  -u 1000 -S -G appsock app \
 && mkdir -p /run/onionshop \
 && chown -R app:appsock /run/onionshop \
 && chmod 750 /run/onionshop
COPY --from=build /out/onionshop /usr/local/bin/onionshop
USER 1000:101
# Read-only rootfs: everything writable is a tmpfs declared in compose
# (/tmp, /run/onionshop). Logs go to stdout; the logging driver keeps them on tmpfs.
ENV ONIONSHOP_SOCKET=/run/onionshop/app.sock
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -q --spider http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/onionshop"]
