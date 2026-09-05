# Build the main binary
FROM --platform=${BUILDPLATFORM} golang:1.22-bookworm as builder

WORKDIR /src
ARG LDFLAGS


COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

ENV CGO_ENABLED=0
ARG TARGETOS
ARG TARGETARCH

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "${LDFLAGS}" -o envoy-split-proxy .


# alpine rather than distroless/static: -iptables shells out to the iptables
# binary, matching how smart-vpn-client manages rules on the same hosts.
FROM alpine:3.21

# iptables for -iptables; iproute2 supplies the `ip` used by the startup bypass
# check, which asks the kernel where bypassed traffic would actually go.
RUN apk upgrade --no-cache && \
    apk add --no-cache iptables iptables-legacy iproute2

WORKDIR /
COPY --from=builder /src/envoy-split-proxy .

ENTRYPOINT ["/envoy-split-proxy"]