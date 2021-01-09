# Build the main binary
FROM golang:1.15.6-buster as builder

WORKDIR /workspace

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o envoy-split-proxy .


FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/envoy-split-proxy .
USER nonroot:nonroot

ENTRYPOINT ["/envoy-split-proxy"]