FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/backup ./cmd/backup

FROM alpine:3.19

RUN apk add --no-cache \
    mongodb-tools \
    ca-certificates \
    tzdata

COPY --from=builder /app/backup /app/backup

RUN addgroup -g 1000 -S appgroup && \
    adduser -u 1000 -S appuser -G appgroup

USER appuser

WORKDIR /app

LABEL org.opencontainers.image.title="mongo-drive-backup" \
      org.opencontainers.image.description="MongoDB Google Drive Backup Service" \
      org.opencontainers.image.source="https://github.com/Sarwarhridoy4/mongo-drive-backup"

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:${WEB_PORT:-8080}/healthz || exit 1

EXPOSE ${WEB_PORT:-8080}

ENTRYPOINT ["/app/backup"]
