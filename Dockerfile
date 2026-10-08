FROM golang:1.26-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/backup \
    ./cmd/backup

FROM alpine:3.22

RUN apk add --no-cache \
    ca-certificates \
    mongodb-tools \
    tzdata \
    wget \
    && addgroup -S -g 1000 appgroup \
    && adduser -S -D -H -u 1000 -G appgroup appuser \
    && mkdir -p /app /tmp/mongodb-backups \
    && test -x /usr/bin/mongodump \
    && test -x /usr/bin/mongorestore \
    && chown -R appuser:appgroup /app /tmp/mongodb-backups

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /out/backup /app/backup

ENV WEB_PORT=8080 \
    TEMP_BACKUP_DIR=/tmp/mongodb-backups \
    MONGODUMP_PATH=/usr/bin/mongodump \
    MONGORESTORE_PATH=/usr/bin/mongorestore

USER appuser

STOPSIGNAL SIGTERM

# The application serves /healthz without dashboard authentication.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${WEB_PORT}/healthz" || exit 1

EXPOSE 8080

ENTRYPOINT ["/app/backup"]
