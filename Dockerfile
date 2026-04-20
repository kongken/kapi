FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o service ./cmd/service

FROM alpine:latest
RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/service ./
COPY config.yaml ./

ENV BUTTERFLY_CONFIG_TYPE=file
ENV BUTTERFLY_CONFIG_FILE_PATH=/root/config.yaml
ENV BUTTERFLY_TRACING_DISABLE=true

CMD ["./service"]
