# Build stage
FROM golang:1.24-alpine AS builder
WORKDIR /workspace
# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download
# Copy source code
COPY cmd/metric-app/ ./cmd/metric-app/
# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o metric-app ./cmd/metric-app/main.go

# Run stage
FROM alpine:3.18
WORKDIR /app
COPY --from=builder /workspace/metric-app .
EXPOSE 8080
CMD ["./metric-app"]
