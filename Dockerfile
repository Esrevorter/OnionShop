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
# No EXPOSE: the app publishes nothing; it listens only on the UNIX socket above.
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD printf 'GET /healthz HTTP/1.0\r\n\r\n' | nc -U /run/onionshop/app.sock | grep -q ok || exit 1
ENTRYPOINT ["/usr/local/bin/onionshop"]
