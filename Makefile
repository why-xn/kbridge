.PHONY: build build-cli build-control-plane build-agent clean proto test test-e2e e2e-setup e2e-teardown kind-up kind-down certs docker docker-control-plane docker-agent docker-cli demo demo-light

# Container image settings (override on the command line, e.g. IMAGE_PREFIX=ghcr.io/acme VERSION=v0.3.0-alpha.1)
IMAGE_PREFIX ?= kbridge
VERSION ?= $(GIT_VERSION)

# Build-time version stamping
VERSION_PKG := github.com/why-xn/kbridge/internal/version
GIT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(VERSION_PKG).Version=$(GIT_VERSION) -X $(VERSION_PKG).Commit=$(GIT_COMMIT) -X $(VERSION_PKG).Date=$(BUILD_DATE)

build: build-cli build-control-plane build-agent

proto:
	./scripts/generate-proto.sh

build-cli:
	go build -ldflags "$(LDFLAGS)" -o bin/kb ./cmd/kb
	ln -sf kb bin/kbridge

build-control-plane:
	go build -ldflags "$(LDFLAGS)" -o bin/kbridge-control-plane ./cmd/controlplane

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

# Render the README demo from docs/assets/demo.tape against the environment that
# scripts/demo-env.sh stood up (needs Docker). The developer logs in live, so
# that home is a throwaway; the admin login comes from DEMO_HOME. demo-light
# renders the same tape in a light theme to demo-light.gif; each render needs a
# fresh environment, since the audit step shows every attempt so far.
DEMO_HOME ?= $(HOME)/kb-demo
VHS = docker run -i --rm --network host -v "$(CURDIR):/vhs" -v "$$(mktemp -d):/home/dev" -v "$(DEMO_HOME)/admin:/home/admin" -v "$(DEMO_HOME)/bin/kb:/usr/local/bin/kb:ro" ghcr.io/charmbracelet/vhs
demo:
	$(VHS) docs/assets/demo.tape
demo-light:
	sed 's/Catppuccin Mocha/Catppuccin Latte/; s#demo\.gif#demo-light.gif#' docs/assets/demo.tape | $(VHS)

# Generate self-signed TLS certs for local development (into ./certs)
certs:
	./scripts/gen-certs.sh certs

# Build all container images
docker: docker-control-plane docker-agent docker-cli

docker-control-plane:
	docker build -f build/control-plane.Dockerfile -t $(IMAGE_PREFIX)-control-plane:$(VERSION) .

docker-agent:
	docker build -f build/agent.Dockerfile -t $(IMAGE_PREFIX)-agent:$(VERSION) .

docker-cli:
	docker build -f build/cli.Dockerfile -t $(IMAGE_PREFIX):$(VERSION) .
