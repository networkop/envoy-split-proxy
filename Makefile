
SOURCES := $(shell find . -name '*.go')
COMMIT := $(shell git describe --dirty --always)
LDFLAGS := "-s -w -X main.GitCommit=$(COMMIT)"
DOCKER_IMAGE ?= networkop/envoy-split-proxy

default: envoy-split-proxy

envoy-split-proxy: $(SOURCES) 
	CGO_ENABLED=0 go build -o envoy-split-proxy -ldflags $(LDFLAGS) .


docker: envoy-split-proxy Dockerfile
	docker buildx build --push \
	--platform linux/amd64,linux/arm64 \
	--build-arg LDFLAGS=$(LDFLAGS) \
	-t $(DOCKER_IMAGE):$(COMMIT) \
	-t $(DOCKER_IMAGE):latest .


lint:
	golangci-lint run

test:
	go test -race ./...  -v
