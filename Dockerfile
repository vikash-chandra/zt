# Stage 1: Build the Go application
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy dependency manifests and download libraries
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go application
RUN CGO_ENABLED=0 GOOS=linux go build -o trading-bot .

# Stage 2: Create a minimal runner image
FROM alpine:latest

WORKDIR /app

# Install ca-certificates in case the bot needs to connect to HTTPS APIs (Zerodha Kite API)
RUN apk --no-cache add ca-certificates tzdata

# Copy the pre-built binary
COPY --from=builder /app/trading-bot .

# Copy environment template (commented out to avoid shadowing container environment variables)
# COPY .env.example .env

# Expose Web Dashboard port
EXPOSE 8080

# Run the binary
CMD ["./trading-bot"]
