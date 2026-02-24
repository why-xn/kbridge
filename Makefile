.PHONY: build build-cli build-central build-agent clean proto test test-e2e e2e-setup e2e-teardown

build: build-cli build-central build-agent

proto:
	./scripts/generate-proto.sh

build-cli:
	go build -o bin/kbridge ./cmd/kbridge

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
