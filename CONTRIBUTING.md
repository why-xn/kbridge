# Contributing to kbridge

Thank you for your interest in contributing. Please read this guide before
opening a pull request.

> **License note:** Contributions are accepted under the
> [Elastic License 2.0](LICENSE). kbridge is source-available, not an
> OSI-approved license. By submitting a pull request you agree that your
> contribution will be licensed under ELv2.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.25+ | Build and test |
| Docker | any recent | E2E test cluster images |
| Kind | 0.23+ | Local Kubernetes for E2E tests |
| helm | 3.x | Render / lint Helm charts |
| protoc + protoc-gen-go + protoc-gen-go-grpc | see `Makefile` | Regenerate gRPC code (only needed for proto changes) |

Install the protoc plugins:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

## Building

```bash
make build          # compile all three binaries to bin/
```

Produces:
- `bin/kb` — CLI (with `bin/kbridge` symlink)
- `bin/kbridge-central` — Central service
- `bin/kbridge-agent` — Cluster agent

## Running tests

```bash
make test           # unit tests with coverage (no external dependencies)
make test-e2e       # end-to-end tests (requires Docker + Kind in PATH)
```

E2E tests spin up a local Kind cluster, build Docker images, deploy via Helm,
and exercise the full login → cluster-select → kubectl flow.

## Proto changes

Edit `api/proto/agent.proto`, then regenerate:

```bash
make proto
```

Never edit files in `api/proto/agentpb/` by hand — they are generated output.

## Code style

- Domain-driven package layout (`internal/central/`, `internal/agent/`, `internal/cli/`).
- Functions stay under 20 lines; prefer composition over inheritance.
- Error messages: lowercase, no trailing punctuation, wrapped with context:
  ```go
  fmt.Errorf("reading config: %w", err)
  ```
- Table-driven unit tests.
- Follow the config pattern every component uses:
  `DefaultConfig()` → `LoadConfig(path)` → `applyEnvOverrides()` → `Validate()`

## Commit style

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <short imperative summary>

[optional body]
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`.

Examples:
```
feat(auth): add refresh-token rotation
fix(agent): handle gRPC reconnect on central restart
docs: update configuration reference for TLS
```

Keep the subject line under 72 characters. Reference GitHub issues with
`Fixes #NNN` in the body when applicable.

## Cutting a release

Releases are triggered by a semver tag. The `release.yml` workflow
publishes compiled binaries + checksums, pushes Docker images to GHCR, and
publishes the Helm charts as an OCI artifact.

```bash
# 1. Make sure CHANGELOG.md has an entry for the new version.
# 2. Tag and push — the workflow does the rest.
git tag -a vX.Y.Z -m "kbridge vX.Y.Z"
git push origin vX.Y.Z
# release.yml publishes binaries+checksums, GHCR images, and OCI charts
```

Only maintainers with push access to the repository can cut a release.
