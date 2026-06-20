.PHONY: build build-cli build-central build-agent clean proto test test-e2e e2e-setup e2e-teardown kind-up kind-down certs docker docker-central docker-agent docker-cli

# Container image settings (override on the command line, e.g. IMAGE_PREFIX=ghcr.io/acme VERSION=v1.0.0)
IMAGE_PREFIX ?= kbridge

# Build-time version stamping
VERSION_PKG := github.com/why-xn/kbridge/internal/version
GIT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(GIT_VERSION) -X $(VERSION_PKG).Commit=$(GIT_COMMIT) -X $(VERSION_PKG).Date=$(BUILD_DATE)

build: build-cli build-central build-agent

proto:
	./scripts/generate-proto.sh

build-cli:
	go build -ldflags "$(LDFLAGS)" -o bin/kb ./cmd/kb
	ln -sf kb bin/kbridge

build-central:
	go build -ldflags "$(LDFLAGS)" -o bin/kbridge-central ./cmd/central

build-agent:
	go build -ldflags "$(LDFLAGS)" -o bin/kbridge-agent ./cmd/agent

clean:
	rm -rf bin/

# Run unit tests
test:
	go test ./... -cover

# Run e2e tests (requires Docker and Kind)
test-e2e:
	./scripts/e2e-kind.sh test

# Setup e2e environment for manual testing
e2e-setup:
	./scripts/e2e-kind.sh setup

# Teardown e2e environment
e2e-teardown:
	./scripts/e2e-kind.sh teardown

# Spin up the Kind cluster only (no services)
kind-up:
	./scripts/e2e-kind.sh cluster-up

# Delete the Kind cluster only
kind-down:
	./scripts/e2e-kind.sh cluster-down

# Generate self-signed TLS certs for local development (into ./certs)
certs:
	./scripts/gen-certs.sh certs

# Build all container images
docker: docker-central docker-agent docker-cli

docker-central:
	docker build -f build/central.Dockerfile -t $(IMAGE_PREFIX)-central:$(VERSION) .

docker-agent:
	docker build -f build/agent.Dockerfile -t $(IMAGE_PREFIX)-agent:$(VERSION) .

docker-cli:
	docker build -f build/cli.Dockerfile -t $(IMAGE_PREFIX):$(VERSION) .
