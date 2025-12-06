FROM golang:1.24 AS builder
WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY apis/ apis/
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a -o metric-collector \
    ./cmd/metriccollector

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/metric-collector .
USER 65532:65532

ENTRYPOINT ["/metric-collector"]
