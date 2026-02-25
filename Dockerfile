FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -o tgloader .

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/tgloader .
COPY --from=builder /app/config.yaml .

# Create directories
RUN mkdir -p downloads cache session

# Expose port (if needed)
EXPOSE 8080

# Run the application
CMD ["./tgloader"]
