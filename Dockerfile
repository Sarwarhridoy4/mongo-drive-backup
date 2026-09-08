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

ENTRYPOINT ["/app/backup"]
