# syntax=docker/dockerfile:1

# =============================================================================
# Stage 1: Go build
# =============================================================================
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Cache dependency downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build agent binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /build/mysql-pitr-agent ./cmd/agent

# Build server binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /build/mysql-pitr-server ./cmd/server

# =============================================================================
# Stage 2: Frontend build
# =============================================================================
FROM node:20-alpine AS frontend

WORKDIR /web

COPY web/package*.json ./
RUN npm ci

COPY web/ .
RUN npm run build

# =============================================================================
# Stage 3: Agent image
# =============================================================================
FROM alpine:3.20 AS agent

# mariadb-client provides mariadb-binlog, which the agent uses to parse local
# binlog files. We locate the binary dynamically (path varies across Alpine /
# MariaDB versions) and expose it as mysqlbinlog for the agent's PATH lookup.
RUN set -e && \
    apk add --no-cache ca-certificates tzdata mariadb-client mariadb-server-utils && \
    BINLOG="$(command -v mariadb-binlog || true)" && \
    if [ -z "$BINLOG" ]; then \
        BINLOG="$(find /usr -type f -name 'mariadb-binlog' 2>/dev/null | head -n1)"; \
    fi && \
    if [ -z "$BINLOG" ]; then \
        echo "ERROR: mariadb-binlog not found after apk add" >&2; exit 1; \
    fi && \
    ln -sf "$BINLOG" /usr/bin/mysqlbinlog && \
    /usr/bin/mysqlbinlog --version >/dev/null && \
    echo "linked mysqlbinlog -> $BINLOG"

COPY --from=builder /build/mysql-pitr-agent /usr/local/bin/mysql-pitr-agent

ENTRYPOINT ["mysql-pitr-agent"]

# =============================================================================
# Stage 4: Server image
# =============================================================================
FROM alpine:3.20 AS server

# The server no longer parses binlogs itself — the agent does — so no
# mysql/mariadb client is needed.
RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /build/mysql-pitr-server /usr/local/bin/mysql-pitr-server

COPY --from=frontend /web/dist /usr/share/mysql-pitr/web/

EXPOSE 8080 9443

ENTRYPOINT ["mysql-pitr-server"]
