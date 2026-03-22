FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o auction-service ./main.go

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/auction-service .

EXPOSE 50051 9090

ENV AUCTION_GRPC_PORT=50051 \
    AUCTION_METRICS_PORT=9090 \
    REDIS_ADDR=redis:6379

ENTRYPOINT ["./auction-service"]
