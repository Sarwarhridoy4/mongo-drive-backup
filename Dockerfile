FROM golang:1.26-alpine

RUN apk add --no-cache \
    mongodb-tools \
    ca-certificates \
    tzdata \
    wget \
    git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/backup ./cmd/backup

RUN addgroup -g 1000 -S appgroup && \
    adduser -u 1000 -S appuser -G appgroup && \
    chown -R appuser:appgroup /app

USER appuser

WORKDIR /app

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:${WEB_PORT:-8080}/healthz || exit 1

EXPOSE ${WEB_PORT:-8080}

ENTRYPOINT ["/app/backup"]