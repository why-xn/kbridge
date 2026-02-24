# kbridge Development Guide

## Project Structure

Three-component architecture: CLI, Central Service, and Agent.

```
cmd/kbridge/          # CLI entry point
cmd/central/          # Central service entry point
cmd/agent/            # Agent entry point
internal/cli/         # CLI commands (Cobra)
internal/central/     # Central service (HTTP + gRPC servers)
internal/agent/       # Agent (gRPC client, kubectl executor)
api/proto/            # Protobuf definitions
api/proto/agentpb/    # Generated gRPC code — do not edit by hand
configs/              # Example YAML configs
scripts/              # Build and test scripts
tests/e2e/            # End-to-end tests (require Kind + Docker)
```

## Build & Test Commands

- `make build` — build all three binaries to `bin/`
- `make test` — run unit tests with coverage
- `make test-e2e` — run E2E tests (requires Docker and Kind)
- `make proto` — regenerate gRPC code from `api/proto/agent.proto`

## Code Style

- Keep functions small (under 20 lines)
- Prefer composition over inheritance
- Write unit tests with table-driven test cases
- Error messages: lowercase, no trailing punctuation, wrap with context (`fmt.Errorf("reading config: %w", err)`)

## Tech Stack & Patterns

- **CLI**: Cobra + Viper (`internal/cli/`)
- **HTTP server**: Gin (`internal/central/http.go`)
- **RPC**: gRPC + Protocol Buffers (`internal/central/grpc.go`, `internal/agent/agent.go`)
- **Config**: YAML files with environment variable overrides

### Config pattern

Every component follows the same pattern — keep it consistent:

```
DefaultConfig() → LoadConfig(path) → applyEnvOverrides() → Validate()
```

### Proto changes

Edit `api/proto/agent.proto`, then run `make proto`. Never edit files in `api/proto/agentpb/` directly.

## Things to Avoid

- Don't over-engineer simple solutions
- Don't add features that weren't asked for
- Don't edit generated protobuf code by hand