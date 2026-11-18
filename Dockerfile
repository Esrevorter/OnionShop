# syntax=docker/dockerfile:1
# OnionShop app image. Production readiness and base-image pinning are tracked
# separately; verify all external image digests before deployment.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.io,direct
RUN apk add --no-cache git && go mod download

# Download pre-compiled sqlc binary (avoids compiling from source, which fails
# in alpine). internal/store/gen/ is gitignored, so the Dockerfile must
# generate it before building the Go binary.
RUN wget -qO /tmp/sqlc.tar.gz https://github.com/sqlc-dev/sqlc/releases/download/v1.27.0/sqlc_1.27.0_linux_amd64.tar.gz \
 && tar -xzf /tmp/sqlc.tar.gz -C /usr/local/bin sqlc \
 && rm /tmp/sqlc.tar.gz

# Copy sqlc configuration (sqlc.yaml) before running sqlc generate.
COPY sqlc.yaml ./

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY db/ ./db/
COPY web/ ./web/

# Generate sqlc bindings from db/schema.sql and db/query/*.sql before building.
# This ensures the image builds correctly even on a clean clone where
# internal/store/gen/ doesn't exist yet.
RUN sqlc generate

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/onionshop ./cmd/onionshop

FROM alpine:3.19
# /run/onionshop MUST exist in the image with the app's ownership: Docker seeds
# an empty named volume from this directory, so the non-root app can create its
# UNIX socket inside the shared appsock volume. Mode 0770 plus nginx's
# supplementary group (docker-compose.yml group_add) is the socket-access
# boundary, mirroring the systemd path's dedicated nginxsock group.
RUN adduser -u 1000 -S app \
 && mkdir -p /run/onionshop \
 && chown 1000:1000 /run/onionshop \
 && chmod 0770 /run/onionshop
COPY --from=build /out/onionshop /usr/local/bin/onionshop
# html/template parses from disk at boot: buildTemplates globs page_*.html under
# web/templates RELATIVE to the working directory. The runtime stage must ship
# them — copying web/ into the build stage alone is not enough.
# web/static is NOT needed here: nginx serves it from a read-only bind mount.
WORKDIR /app
COPY --from=build /src/web/templates ./web/templates
USER 1000:1000
ENV ONIONSHOP_ADDR=127.0.0.1:8080
EXPOSE 8080
# UNIX socket is used by Compose. Set a restrictive umask while retaining
# group write for nginx's dedicated socket-access group.
ENTRYPOINT ["/bin/sh", "-c", "umask 0007; exec /usr/local/bin/onionshop \"$@\"", "--"]
CMD []
