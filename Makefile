
SOURCES := $(shell find . -name '*.go')
DOCKER_IMAGE ?= networkop/envoy-split-proxy

default: envoy-split-proxy

envoy-split-proxy: $(SOURCES) 
	CGO_ENABLED=0 go build -o envoy-split-proxy -ldflags "-X main.version=$(VERSION) -extldflags -static" .


docker_build: envoy-split-proxy Dockerfile
	docker build -t $(DOCKER_IMAGE)  .

docker_push:
	docker push $(DOCKER_IMAGE)