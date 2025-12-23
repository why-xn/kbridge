.PHONY: build build-cli build-central build-agent clean proto

build: build-cli build-central build-agent

proto:
	./scripts/generate-proto.sh

build-cli:
	go build -o bin/mk8s ./cmd/mk8s

build-central:
	go build -o bin/mk8s-central ./cmd/central

build-agent:
	go build -o bin/mk8s-agent ./cmd/agent

clean:
	rm -rf bin/
