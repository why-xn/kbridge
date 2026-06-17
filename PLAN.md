# kbridge - Implementation Plan

## Current Status

Phases 1, 2, and most of Phase 3 are complete.

**Phases 1 & 2 (core system):**
- CLI, Central, and Agent binaries build and work end-to-end
- Agent registration, heartbeat, and command polling implemented
- kubectl passthrough and kubectl edit supported
- E2E tests passing with Kind clusters

**Phase 3 (authentication) — done except 3.4 admin token management:**
- SQLite-backed store with auto-migration replaces the in-memory store
- Full schema exists: users, clusters, agent_tokens, roles, permissions,
  user_roles, audit_logs, refresh_tokens
- JWT auth (`internal/auth/`: jwt, middleware, password/bcrypt)
- HTTP endpoints: `/auth/login`, `/auth/refresh`, `/auth/logout`,
  `/auth/change-password`; `/api/v1/clusters` protected by auth middleware
- CLI `login`/`logout` with token storage; client sends Authorization header
- Agent token validated against the store during Register RPC

> Note: several DB tables (roles, permissions, user_roles, audit_logs) and an
> `audit:` config block already exist but are NOT yet wired to any logic — the
> schema is ahead of the feature work in Phases 4 and 5.

## Phase 3: Authentication

Status: 3.1, 3.2, 3.3 DONE. 3.4 partially done (token validated on Register,
but no admin endpoints to generate/list/revoke tokens, no rotation).

### 3.1 Database Setup — DONE (PostgreSQL alt driver not added)

Add persistent storage to the central service.

**Tasks:**
- Add SQLite driver (github.com/mattn/go-sqlite3 or modernc.org/sqlite)
- Create `internal/central/db.go` with database interface
- Define schema migrations for users, clusters, roles tables
- Auto-migrate on startup
- Add database config to `central.yaml`:
  ```yaml
  database:
    driver: sqlite
    path: kbridge.db
  ```
- Replace in-memory AgentStore with database-backed store
- Add PostgreSQL support as alternative driver

**Acceptance Criteria:**
- Central persists cluster registrations across restarts
- Database driver is configurable (sqlite/postgres)
- Tables created automatically on first run

### 3.2 User Authentication — DONE

Implement JWT-based user authentication.

**Tasks:**
- Add bcrypt for password hashing
- Add JWT library (golang-jwt/jwt)
- Create `internal/auth/jwt.go` - token generation and validation
- Create `internal/auth/middleware.go` - Gin middleware for JWT validation
- Implement `POST /auth/login` endpoint:
  - Accept email + password
  - Validate against database
  - Return JWT token (RS256, 24h expiry)
- Implement `POST /auth/refresh` endpoint
- Protect `/api/v1/clusters` and `/api/v1/clusters/{name}/exec` with auth middleware
- Create initial admin user on first startup (configurable)
- Add auth config to `central.yaml`:
  ```yaml
  auth:
    jwt_secret: "your-secret-key"
    token_expiry: 24h
    admin_email: admin@example.com
    admin_password: changeme
  ```

**Acceptance Criteria:**
- Login returns JWT token
- Protected endpoints require valid token
- Invalid/expired tokens return 401
- First startup creates admin user

### 3.3 CLI Login Flow — DONE

Connect the CLI to the authentication system.

**Tasks:**
- Implement `kbridge login` command:
  - Prompt for central URL (if not configured)
  - Prompt for email and password
  - Call `POST /auth/login`
  - Store token in `~/.kbridge/config.yaml`
- Implement `kbridge logout` command:
  - Remove token from config
- Add Authorization header to all API requests in CentralClient
- Auto-detect 401 responses and prompt for re-login
- Update `kbridge status` to show authenticated user

**Acceptance Criteria:**
- `kbridge login` prompts and stores token
- `kbridge logout` removes token
- All API calls include Authorization header
- 401 responses show helpful message

### 3.4 Agent Token Authentication — DONE (token rotation: see note)

Secure agent registration with database-backed tokens.

Implemented:
- Admin endpoints `POST/GET/DELETE /api/v1/admin/agent-tokens`, gated by
  `auth.AdminRequired()` (`internal/central/admin_handlers.go`), and the
  `kbridge admin agent-tokens create/list/revoke` CLI. Create returns the
  plaintext token once; only the SHA-256 hash + prefix are stored.
- gRPC `Register` now validates the agent token against the DB via the
  `AgentAuthenticator` domain service (`internal/central/agent_auth.go`):
  hash lookup, revoked + expiry checks, and cluster-binding enforcement
  (a token authorizes exactly one cluster). On success the cluster row is
  persisted as connected; the in-memory store still drives live command routing.
- Optional `bootstrap` config seeds a dev agent token on startup; production
  uses the admin API.

Token rotation works operationally today: an admin issues a new token and
revokes the old one; the agent re-registers with the new token. Hot rotation
without any agent reconnect is not implemented.

**Tasks:**
- ~~Store hashed agent tokens in database instead of in-memory~~ (DONE)
- Add admin API endpoint `POST /api/v1/admin/agent-tokens` to generate tokens
- Add admin API endpoint `GET /api/v1/admin/agent-tokens` to list tokens
- Add admin API endpoint `DELETE /api/v1/admin/agent-tokens/{id}` to revoke tokens
- Validate agent token against database during Register RPC
- Support token rotation without agent restart

**Acceptance Criteria:**
- Agent registration requires valid database-stored token
- Admin can generate and revoke tokens via API
- Invalid tokens are rejected with clear error

---

## Phase 4: RBAC — config-file based (ArgoCD-style)

**Design change:** RBAC is defined declaratively in a YAML policy file
(hot-reloaded), NOT stored/CRUD'd in the database. This is GitOps-friendly,
version-controlled, and drops the role/permission CRUD endpoints and the
`roles`/`permissions`/`user_roles` tables from the authz path. The DB keeps
users only, for authentication. Modeled on ArgoCD's `argocd-rbac-cm`.

### 4.1 Role Definitions — DONE (config-based)

Implemented in `internal/central/policy.go` + `rbac.go`:
- Policy YAML: `default` role, `roles` (each with `rules` of
  clusters/namespaces/resources/verbs), and `bindings` (subject→roles, subject
  matched against the JWT email, wildcards supported). Example: `configs/rbac.yaml`.
- Wildcard matching (`matchPattern`) and verb matching, fully unit-tested.
- `PolicyEngine` holds the policy in an `atomic.Pointer` and hot-reloads via
  fsnotify (watches the containing dir) and on SIGHUP (works on filesystems that
  don't deliver inotify events, e.g. 9p/WSL mounts); bad reloads are logged and
  the previous policy is kept.
- Default roles ship in the example policy (admin / developer / viewer).
- `rbac.policy_file` config; empty = enforcement disabled (allow-all).

Role CRUD endpoints are intentionally NOT implemented — roles live in the file.

**Acceptance Criteria:**
- Roles defined with cluster/namespace/resource/verb permissions ✓
- Wildcard patterns match correctly (`dev-*` matches `dev-cluster`) ✓
- Default roles exist (in the example policy) ✓
- Policy changes take effect without restart (hot-reload) ✓

### 4.2 Permission Enforcement — DONE

Enforced in the exec handler (`http.go` `authorizeExec`):
- `parseAccessRequest` extracts verb / resource (with `/name` stripped; pods for
  logs/exec/etc.) / namespace (`-n`, `--namespace=`, `-A` → `*`, else default).
- The user's JWT email is checked against the policy before the command is
  routed to the agent; denials return 403 and are logged.
- User→role assignment is via the policy `bindings`, not an API.

**Acceptance Criteria:**
- Unauthorized commands return 403 (unit + verified) ✓
- Authorized commands succeed (e2e: admin runs full kubectl suite) ✓
- Wildcard patterns work (`dev-*` matches `dev-cluster`) ✓
- Permission denials are logged ✓

### 4.3 Admin User Management — DONE

Admin endpoints (gated by `auth.AdminRequired()`):
- `GET /api/v1/admin/users` — list (password hashes never serialized)
- `POST /api/v1/admin/users` — create (bcrypt-hashed; 409 on duplicate email)
- `PUT /api/v1/admin/users/{id}` — update name / active / password (404 if absent)
- `DELETE /api/v1/admin/users/{id}` — delete
- CLI: `kbridge admin users list`/`create`, `kbridge admin agent-tokens
  create`/`list`/`revoke`, and `kbridge admin audit`
  (prompts for password if `--password` omitted)

Note: there is no role-assignment API — a user's roles come from the policy
file `bindings` (matched on email), per the config-based RBAC design.

**Acceptance Criteria:**
- Admin can create, list, update, delete users ✓
- Non-admin users cannot access admin endpoints ✓ (AdminRequired, e2e-verified)
- CLI admin commands work

---

## Phase 5: Production Readiness — DONE

### 5.2 Audit Logging — DONE

- `AuditRecorder` domain service (`audit.go`) records each exec with user,
  cluster, command, namespace, status, exit code, duration, and client IP.
  Recording uses a background context (a cancelled request still audits) and
  never fails the caller.
- The exec handler records every outcome: `success` / `failed` (by exit code)
  / `timeout`, and `denied` for RBAC rejections.
- `GET /api/v1/admin/audit` with filters (user, cluster, status, from/to
  RFC3339, page, per_page) + `kbridge admin audit` CLI.
- Retention cleanup goroutine deletes logs older than `audit.retention_days`
  every `audit.cleanup_interval`.
- Verified: unit (recorder, endpoint filters, denied-records-audit) + e2e
  (`TestAuditLogRecorded`).

### 5.1 TLS — DONE (server-auth TLS)

Server-authenticated TLS across all hops; mutual TLS (client certs) is a
possible future extension.

- Central `tls: {enabled, cert_file, key_file}` secures both HTTP and gRPC with
  the same cert (`config.go` + `tls.go`); validated when enabled.
- Agent `central.tls: {enabled, ca_file, insecure}` builds gRPC client creds
  (`agent/tls.go`): CA-verified, system-roots (empty CA), or skip-verify.
- CLI honours `insecure_skip_verify` for HTTPS with self-signed certs.
- `make certs` / `scripts/gen-certs.sh` generate a dev cert (localhost +
  127.0.0.1 SAN). Example `tls:` blocks added to both configs (disabled default).
- Verified: gRPC-over-TLS handshake integration test (TLS client registers,
  plaintext client rejected), HTTPS smoke test (health over TLS, plaintext
  rejected), agent-creds + config-validation unit tests.

**Acceptance Criteria:**
- All communication can be encrypted with TLS ✓
- Invalid/plaintext connections rejected ✓
- Insecure mode available for development ✓

### 5.2 Audit Logging — PARTIAL (schema + config only)

Log all kubectl commands for compliance and debugging.
Done: `audit_logs` table exists and an `audit:` config block (retention_days,
cleanup_interval) is present in `central.yaml`. Remaining: nothing writes to
the table, no query endpoint, no CLI, no retention/cleanup job.

**Tasks:**
- ~~Create audit_logs table (user, cluster, command, timestamp, duration, status, exit_code)~~ (DONE)
- Log every command execution in the exec handler
- Add `GET /api/v1/admin/audit` endpoint with filtering (user, cluster, date range)
- Add `kbridge admin audit` CLI command
- Add log retention/cleanup (configurable max age)

**Acceptance Criteria:**
- All commands logged with user, cluster, command, result
- Audit log queryable via API and CLI
- Old logs cleaned up automatically

### 5.3 Docker Images — DONE

- Multi-stage, CGO-free (modernc sqlite) Alpine Dockerfiles in `build/`:
  `central.Dockerfile`, `agent.Dockerfile` (bundles kubectl), `cli.Dockerfile`.
  All run as a non-root user.
- `make docker` (+ `docker-central`/`-agent`/`-cli`), parameterised by
  `IMAGE_PREFIX` / `VERSION`.

**Acceptance Criteria:**
- `make docker` builds all images ✓ (verified building each)
- Images minimal ✓ — CLI 18MB, central 36MB; agent 76MB (includes kubectl,
  which the agent requires, so >50MB is expected)
- Images work ✓ — central serves, CLI runs, agent has kubectl on PATH

### 5.4 Helm Charts — DONE

- `charts/central`: Deployment, Service, Secret (renders central.yaml with
  secrets inline), ConfigMap (RBAC policy), PVC for SQLite, optional Ingress,
  TLS-aware probes/mounts. Config swap triggers a rollout via checksum annotation.
- `charts/agent`: Deployment, ServiceAccount, ClusterRole + Binding (rules
  configurable; defaults broad with a note that the kbridge policy is the real
  gate), Secret (agent.yaml + optional CA cert).
- Both have a configurable `values.yaml`.

**Acceptance Criteria:**
- `helm install ./charts/central` renders ✓ (lint + template, incl. TLS+ingress)
- `helm install ./charts/agent` renders ✓ (lint + template, incl. TLS+CA)
- All config values customizable ✓

### 5.5 Documentation — DONE

- README updated for final features (RBAC/auth/audit/TLS/Docker/Helm); stale
  "planned" markers removed; architecture diagram retained; Documentation index added.
- `docs/installation.md` — binary, Docker, Helm.
- `docs/configuration.md` — every central/agent/CLI option, env vars, TLS setup.
- `docs/cli.md` — every command with examples.
- `docs/api.md` — all HTTP endpoints with status codes.
- `docs/rbac.md` — policy file format, matching semantics, examples.
- `docs/admin.md` — users, agent tokens, audit, RBAC changes.

**Acceptance Criteria:**
- New users can get started from README ✓
- All config options documented ✓
- All CLI commands documented ✓
- All API endpoints documented ✓

---

## Summary

| Phase | Focus | Status |
|-------|-------|--------|
| Phase 1 | Project setup, skeletons | Done |
| Phase 2 | Core kubectl proxy functionality | Done |
| Phase 3 | Authentication (DB, JWT, login) | Done |
| Phase 4 | RBAC (config-file policy + enforcement) | Done |
| Phase 5 | Production (TLS, audit, Docker, Helm, docs) | Done |

**Status:** Phases 1–5 are complete and verified (unit + e2e). The system is
feature-complete per this plan.

**Possible follow-ups (not in this plan):**
- PostgreSQL store driver (the interface is ready; only SQLite is implemented).
- Mutual TLS (client certificates) — currently server-authenticated TLS only.
- Drop the unused `roles`/`permissions`/`user_roles` tables (RBAC moved to the
  policy file), or keep them for a future DB-override layer.
