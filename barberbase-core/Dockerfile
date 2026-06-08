# Stage 1: Build
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install git and build dependencies
RUN apk add --no-cache git make

# Copy go.mod and go.sum
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Build the API binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o bin/server ./cmd/server/main.go

# Stage 2: Run
FROM alpine:latest

WORKDIR /app

# Add ca-certificates and timezone data
RUN apk add --no-cache ca-certificates tzdata

# Copy the binary from builder
COPY --from=builder /app/bin/server .

# Copy migrations so they can be run on startup
COPY --from=builder /app/migrations ./migrations

# Expose port
EXPOSE 8080

# Run the API
CMD ["./server"]
