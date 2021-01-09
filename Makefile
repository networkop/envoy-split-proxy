
SOURCES := $(shell find . -name '*.go')
DOCKER_IMAGE ?= networkop/envoy-split-proxy

default: envoy-split-proxy

envoy-split-proxy: $(SOURCES) 
	CGO_ENABLED=0 go build -o envoy-split-proxy -ldflags "-X main.version=$(VERSION) -extldflags -static" .


docker: envoy-split-proxy Dockerfile
	docker buildx build --push --platform linux/amd64,linux/arm64 -t $(DOCKER_IMAGE)  .
