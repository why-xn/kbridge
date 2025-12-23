.PHONY: build build-cli build-central build-agent clean

build: build-cli build-central build-agent

build-cli:
	go build -o bin/mk8s ./cmd/mk8s

build-central:
	go build -o bin/mk8s-central ./cmd/central

build-agent:
	go build -o bin/mk8s-agent ./cmd/agent

clean:
	rm -rf bin/
