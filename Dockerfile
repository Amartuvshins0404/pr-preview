FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pr-preview ./cmd/pr-preview

FROM alpine:3.22 AS railpack
ARG RAILPACK_VERSION=0.43.0
ARG TARGETARCH
RUN apk add --no-cache curl \
 && case "$TARGETARCH" in arm64) arch=arm64 ;; amd64) arch=x86_64 ;; *) echo "unsupported arch $TARGETARCH" && exit 1 ;; esac \
 && file="railpack-v${RAILPACK_VERSION}-${arch}-unknown-linux-musl.tar.gz" \
 && base="https://github.com/railwayapp/railpack/releases/download/v${RAILPACK_VERSION}" \
 && curl -fsSLO "$base/$file" \
 && curl -fsSL "$base/checksums.txt" | grep " $file\$" | sha256sum -c - \
 && tar -xzf "$file" -C /usr/local/bin railpack

FROM docker:29-cli
ARG RAILPACK_VERSION=0.43.0
RUN apk add --no-cache git ca-certificates
COPY --from=railpack /usr/local/bin/railpack /usr/local/bin/railpack
COPY --from=build /out/pr-preview /usr/local/bin/pr-preview
ENV RAILPACK_FRONTEND=ghcr.io/railwayapp/railpack-frontend:v${RAILPACK_VERSION}
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pr-preview"]
