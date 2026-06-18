.PHONY: build build-cli build-central build-agent clean proto test test-e2e e2e-setup e2e-teardown kind-up kind-down certs docker docker-central docker-agent docker-cli

# Container image settings (override on the command line, e.g. IMAGE_PREFIX=ghcr.io/acme VERSION=v1.0.0)
IMAGE_PREFIX ?= kbridge
VERSION ?= latest

build: build-cli build-central build-agent

proto:
	./scripts/generate-proto.sh

build-cli:
	go build -o bin/kb ./cmd/kb
	ln -sf kb bin/kbridge

build-central:
	go build -o bin/kbridge-central ./cmd/central

build-agent:
	go build -o bin/kbridge-agent ./cmd/agent

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
