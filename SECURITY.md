# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.0.x   | Yes       |

Older versions receive no security fixes. Please upgrade to the latest 1.0.x
patch release.

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Report security issues privately through
[GitHub Security Advisories](https://github.com/why-xn/kbridge/security/advisories/new).
You can also reach the maintainers at **shihabhasan.iut@gmail.com** if you
prefer email.

Please include:
- A description of the vulnerability and its potential impact.
- Steps to reproduce or a proof-of-concept (if safe to share).
- The version(s) affected.

You will receive an acknowledgement within 5 business days. We aim to release a
patch within 30 days of a confirmed report, depending on severity and complexity.

We follow coordinated disclosure: please give us a reasonable window to prepare
a fix before public disclosure.

## Security Posture

### Transport security

- **Server-authenticated TLS** is supported on both the HTTP REST API and the
  agent↔central gRPC channel. Enable it in `central.yaml` under `tls:`.
- **mTLS** (mutual TLS for agents) is planned but currently deferred. The
  channel is protected by server-auth TLS only.

### Authentication and secrets

- Users authenticate with JWT access tokens (HS256, 1 h TTL) plus opaque
  refresh tokens. The CLI transparently rotates expired tokens.
- `jwt_secret` and `token_pepper` must be at least 32 characters; the server
  refuses to start with a shorter value (fail-closed).
- Secrets can be supplied as a mounted file (recommended in Kubernetes), an
  environment variable, or an inline config value.
- **Agent tokens** are high-entropy random secrets shown once at creation time
  and stored only as an HMAC-SHA256 digest keyed by the server-side
  `token_pepper`. A stolen database alone cannot be used to verify guessed
  tokens.
- Login attempts are rate-limited per IP to slow brute-force attacks.

### Audit logging

Every kubectl command — whether allowed, denied, failed, or timed out — is
recorded with the user identity, cluster, full command, result, and duration.
Logs are queryable by admins via `kb admin audit` or
`GET /api/v1/admin/audit`.

### Helm chart hardening

The official Helm charts ship with:
- Non-root user and group (`runAsNonRoot: true`).
- Read-only root filesystem.
- All Linux capabilities dropped.
- Agent liveness probed via an exec check against the heartbeat health file.
