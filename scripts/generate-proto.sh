#!/bin/bash
# generate-proto.sh - Generates Go code from protobuf definitions
#
# Prerequisites:
#   - protoc (Protocol Buffer Compiler) must be installed
#   - Go must be installed
#
# Usage:
#   ./scripts/generate-proto.sh

set -e

# Ensure we're in the project root
cd "$(dirname "$0")/.."

# Add Go bin directories to PATH for protoc plugins
export PATH="$PATH:$(go env GOPATH)/bin:$HOME/.local/bin"

echo "Installing protoc-gen-go and protoc-gen-go-grpc..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Check for protoc
if ! command -v protoc &> /dev/null; then
    echo "Error: protoc is not installed or not in PATH"
    echo "Please install Protocol Buffers compiler: https://grpc.io/docs/protoc-installation/"
    exit 1
fi

# Create output directory
mkdir -p api/proto/agentpb

echo "Generating Go code from proto files..."
protoc --go_out=api/proto/agentpb --go_opt=paths=source_relative \
       --go-grpc_out=api/proto/agentpb --go-grpc_opt=paths=source_relative \
       --proto_path=api/proto \
       agent.proto

echo "Proto generation complete!"
echo "Generated files:"
ls -la api/proto/agentpb/
