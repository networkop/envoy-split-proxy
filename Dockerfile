# Build the main binary
FROM --platform=${BUILDPLATFORM} golang:1.15.6-buster as builder

WORKDIR /src
ARG LDFLAGS


COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -ldflags "${LDFLAGS}" -o envoy-split-proxy .


FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /src/envoy-split-proxy .
USER nonroot:nonroot

ENTRYPOINT ["/envoy-split-proxy"]