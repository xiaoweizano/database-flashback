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

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /build/mysql-pitr-agent /usr/local/bin/mysql-pitr-agent

ENTRYPOINT ["mysql-pitr-agent"]

# =============================================================================
# Stage 4: Server image
# =============================================================================
FROM alpine:3.20 AS server

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /build/mysql-pitr-server /usr/local/bin/mysql-pitr-server

COPY --from=frontend /web/dist /usr/share/mysql-pitr/web/

EXPOSE 8080

ENTRYPOINT ["mysql-pitr-server"]
