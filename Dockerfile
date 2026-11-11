# syntax=docker/dockerfile:1
# OnionShop app image. Production readiness and base-image pinning are tracked
# separately; verify all external image digests before deployment.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY db/ ./db/
COPY web/ ./web/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/onionshop ./cmd/onionshop

FROM alpine:3.19
RUN adduser -u 1000 -S app
COPY --from=build /out/onionshop /usr/local/bin/onionshop
USER 1000:1000
ENV ONIONSHOP_ADDR=127.0.0.1:8080
EXPOSE 8080
# UNIX socket is used by Compose. Set a restrictive umask while retaining
# group write for nginx's dedicated socket-access group.
ENTRYPOINT ["/bin/sh", "-c", "umask 0007; exec /usr/local/bin/onionshop \"$@\"", "--"]
CMD []
