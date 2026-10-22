# Build do microsserviço Go.
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o irrigacao-mss .

# Runtime enxuto com certificados TLS e dados de timezone.
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /app/irrigacao-mss .
EXPOSE 8456
CMD ["./irrigacao-mss"]
