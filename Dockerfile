# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go ./
COPY pkg/ ./pkg/

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o vpa-controller main.go

# Final stage
FROM alpine:3.19

WORKDIR /

# Copy the binary from the builder stage
COPY --from=builder /app/vpa-controller /vpa-controller

# Use a non-root user
USER 65532:65532

ENTRYPOINT ["/vpa-controller"]
