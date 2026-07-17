# syntax=docker/dockerfile:1.7
FROM golang:1.26.2-alpine AS builder

RUN apk add --no-cache ca-certificates

ENV CGO_ENABLED=0
WORKDIR /src

# Cache dependency resolution separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p /out && \
    go build \
      -trimpath \
      -ldflags "-X github.com/sourcegraph/zoekt.Version=$VERSION" \
      -o /out/ \
      ./cmd/...

FROM alpine:3.24

RUN apk add --no-cache git ca-certificates bind-tools tini jansson wget && \
    version=$(git --version | awk '{print $3}' | cut -d- -f1) && \
    major=$(echo "$version" | cut -d. -f1) && \
    minor=$(echo "$version" | cut -d. -f2) && \
    if [ "$major" -lt 2 ] || { [ "$major" -eq 2 ] && [ "$minor" -lt 52 ]; }; then \
      echo "git $version is below minimum 2.52.0" >&2; exit 1; \
    fi && \
    echo "git $version (>= 2.52.0)"

COPY --chmod=755 install-ctags-alpine.sh /usr/local/bin/install-ctags-alpine.sh
RUN /usr/local/bin/install-ctags-alpine.sh && \
    rm /usr/local/bin/install-ctags-alpine.sh \
      /usr/local/bin/universal-optscript

RUN addgroup -S zoekt && \
    adduser -S -G zoekt -h /home/zoekt zoekt && \
    mkdir -p /data/index /home/zoekt && \
    chown -R zoekt:zoekt /data /home/zoekt

COPY --from=builder /out/ /usr/local/bin/

USER zoekt
WORKDIR /home/zoekt

ENV DATA_DIR=/data/index

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["zoekt-webserver", "-index", "/data/index", "-pprof", "-rpc"]
