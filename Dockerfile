# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/backupfabric \
    ./cmd/backupfabric

FROM ubuntu:24.04

ARG UID=10001
ARG GID=10001
ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        openssh-client \
        rsync \
        tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${GID}" backupfabric \
    && useradd --uid "${UID}" --gid backupfabric \
        --home-dir /var/lib/backupfabric --no-create-home \
        --shell /usr/sbin/nologin backupfabric \
    && install -d -o backupfabric -g backupfabric -m 0700 /var/lib/backupfabric

COPY --from=build /out/backupfabric /usr/local/bin/backupfabric

USER backupfabric
WORKDIR /var/lib/backupfabric
VOLUME ["/var/lib/backupfabric"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["curl", "--fail", "--silent", "http://127.0.0.1:8080/api/v1/health"]

ENTRYPOINT ["/usr/local/bin/backupfabric"]
CMD ["serve", "--listen", "127.0.0.1:8080", "--database", "/var/lib/backupfabric/backupfabric.db"]
