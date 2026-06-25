# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-06-20

### Added

- **Multi-cluster kubectl proxy** — central service + per-cluster agents connected via outbound gRPC; no inbound firewall rules or VPN required.
- **CLI (`kb`)** — kubectl-compatible command-line tool; anything that isn't a management command is forwarded to the selected cluster. `kbridge` remains as a back-compat symlink.
- **JWT authentication** — HS256 access tokens (1 h TTY) with opaque refresh tokens; the CLI transparently refreshes expired tokens so interactive sessions are uninterrupted.
- **Login rate limiting** — failed login attempts are rate-limited per IP to slow brute-force attacks; the server fails closed (startup aborted) if rate-limiter bounds cannot be satisfied.
- **Declarative file-based RBAC** — hot-reloaded policy file (ArgoCD-style) with roles, wildcarded rules (cluster / namespace / resource / verb), and user bindings by JWT email.
- **Audit logging** — every command (allowed, denied, failed, or timed out) recorded with user, cluster, command, result, and duration; queryable via `kb admin audit` and `GET /api/v1/admin/audit`.
- **Streaming logs and watch** — `kb logs -f` / `kb get pods -w` stream output live to the terminal until Ctrl-C.
- **Interactive exec** — `kb exec -it` allocates a full PTY for interactive shells; `-i` provides stdin-only mode.
- **Port-forward** — `kb port-forward` tunnels a pod port to localhost.
- **Server-authenticated TLS** — configurable for the HTTP REST API and the agent↔central gRPC channel; `make certs` generates a dev certificate.
- **Agent heartbeat health file + exec probe** — the agent writes a liveness file on every heartbeat; the Helm chart configures an exec-based liveness probe against it.
- **Secrets from file, environment variable, or inline** — `jwt_secret`, `token_pepper`, and agent tokens can be supplied as a file path (`_file` suffix), an env var, or a plain inline value; secrets shorter than 32 characters are rejected at startup.
- **Build-time version stamping** — `--version` flag on all three binaries; version, commit, and build date injected via `-ldflags` at `make build` time.
- **Helm charts** — production-grade charts for central and agent; support `existingSecret`, pod security contexts, central `token_pepper` and `streams` config, and the agent heartbeat exec probe.
- **End-to-end test suite** — Kind-based E2E tests covering login rate limiting, secret-from-file boot, and fail-closed startup (`make test-e2e`).

### Changed

- SQLite concurrency hardened: `busy_timeout` set and a single-writer serialisation lock applied to prevent `SQLITE_BUSY` errors under concurrent requests.
- Access token lifetime reduced to 1 hour (previously longer-lived); refresh rotation keeps sessions alive transparently.
- Helm chart: agent and central now read secrets from mounted files by default when `existingSecret` is configured, avoiding environment-variable exposure.
- Helm chart: pod security contexts tightened (non-root user, read-only root filesystem, dropped capabilities).
- Trusted-proxy configuration added to the Gin HTTP server to ensure accurate client-IP detection behind a load balancer or ingress.

### Security

- **Strong-secret enforcement (fail-closed)** — the central service refuses to start if `jwt_secret` or `token_pepper` is shorter than 32 characters, preventing accidental weak-key deployments.
- **Agent token storage** — tokens are stored only as HMAC-SHA256 digests keyed by a server-side pepper; a stolen database alone cannot be used to verify guessed tokens.
- mTLS between agents and central is planned but deferred; the channel is currently protected by server-authenticated TLS only.

[Unreleased]: https://github.com/why-xn/kbridge/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/why-xn/kbridge/releases/tag/v1.0.0
