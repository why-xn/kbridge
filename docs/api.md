# HTTP API Reference

Base URL: the central service (`http(s)://host:8080`). All `/api/v1/*` routes
require a `Authorization: Bearer <access_token>` header; `/api/v1/admin/*` also
requires the `admin` role. Tokens come from `POST /auth/login`.

## Health

### `GET /health`
Unauthenticated. Returns `{"status":"healthy"}`.

## Auth

### `POST /auth/login`
Body: `{"email","password"}`. Returns `{access_token, refresh_token, expires_in}`.
`401` on bad credentials, `403` if the account is disabled.

### `POST /auth/refresh`
Body: `{"refresh_token"}`. Returns a new token pair (the old refresh token is
rotated out). `401` if invalid/expired.

### `POST /api/v1/auth/logout`
Body: `{"refresh_token"}`. Invalidates the refresh token.

### `POST /api/v1/auth/change-password`
Body: `{"current_password","new_password"}`. Changes the caller's password.

## Clusters

### `GET /api/v1/clusters`
Lists clusters and their status.

### `POST /api/v1/clusters/{name}/exec`
Runs a kubectl command. Body:

```json
{ "command": ["get","pods"], "namespace": "default", "timeout": 30, "stdin": "" }
```

Returns `{output, exit_code, error}`. Status codes:

| Code | Meaning |
|------|---------|
| 200 | Command executed (check `exit_code`) |
| 403 | Denied by RBAC policy |
| 404 | Cluster not found |
| 503 | Cluster agent disconnected |
| 504 | Command timed out |

Every call is recorded in the audit log.

### `POST /api/v1/clusters/{name}/stream`
Streams a follow/watch command (`logs -f`, `get -w`). Same request body as
`exec`. RBAC is checked before the stream starts (403 on denial). On success
returns `200` with a chunked response body that streams stdout/stderr until the
command ends or the client disconnects (which cancels it). Status codes: `403`
denied, `404` cluster not found, `503` agent disconnected or no open stream,
`429` over `streams.max_concurrent`. The outcome is audited as `success`,
`failed`, or `canceled`.

### `POST /api/v1/clusters/{name}/exec/attach`
Opens an interactive exec session over an HTTP/2 bidirectional stream. This is
the attach endpoint — distinct from the one-shot `/exec` and the streaming
`/stream` endpoints.

**Query parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `pod` | yes | Pod name |
| `container` | no | Container name (defaults to first container) |
| `namespace` | no | Kubernetes namespace |
| `command` | yes (repeated) | Remote command and arguments, one per param |
| `tty` | no | `true` to allocate a PTY |
| `rows` | no | Initial terminal height (rows) |
| `cols` | no | Initial terminal width (columns) |

**Auth:** `Authorization: Bearer <jwt>`. RBAC must grant the `exec` verb on
`pods` for the target cluster and namespace.

**Frame protocol.** On `200` the response body is a length-prefixed frame
stream. Frames from the client (stdin data, terminal resize events) flow
upstream; frames from the server (stdout, stderr, exit status) flow downstream.
The stream ends when the remote process exits or the client disconnects.

**Status codes:**

| Code | Meaning |
|------|---------|
| 200 | Session established; frame stream follows |
| 403 | Denied by RBAC policy |
| 404 | Cluster not found |
| 429 | Over `streams.max_concurrent` limit |
| 503 | Cluster agent disconnected |

Every session is recorded in the audit log with outcome `success`, `failed`,
`denied`, or `canceled`.

### `POST /api/v1/clusters/{name}/port-forward`
Opens a port-forward session over an HTTP/2 bidirectional stream. Local TCP
connections made by the CLI are multiplexed over this single stream and tunneled
through central to the agent, which forwards them to the pod via kubectl.

**Query parameters:**

| Parameter | Required | Description |
|-----------|----------|-------------|
| `pod` | yes | Pod name |
| `namespace` | no | Kubernetes namespace |
| `port` | no (repeated) | Remote port numbers, one per param |

**Auth:** `Authorization: Bearer <jwt>`. RBAC must grant the `port-forward` verb
on `pods` for the target cluster and namespace.

**Frame protocol.** On `200` the response body is a length-prefixed frame stream
shared by all connections in the session. Each frame carries a `conn_id` that
identifies the individual TCP connection. Frame types:

| Type | Direction | Description |
|------|-----------|-------------|
| `READY` | server → client | Session is up; forwarding may begin |
| `OPEN` | client → server | New connection for `conn_id` on `remote_port` |
| `DATA` | both | Payload bytes for `conn_id` |
| `CLOSE` | both | Half-close or full teardown of `conn_id` |
| `CONN_ERROR` | server → client | Error for a single `conn_id`; other connections continue |
| `SESSION_ERROR` | server → client | Fatal session error; stream ends |

**Status codes:**

| Code | Meaning |
|------|---------|
| 200 | Session established; frame stream follows |
| 403 | Denied by RBAC policy |
| 404 | Cluster not found |
| 429 | Over `streams.max_concurrent` limit |
| 503 | Cluster agent disconnected |

Every session is recorded in the audit log.

## Admin — agent tokens

### `POST /api/v1/admin/agent-tokens`
Body: `{"cluster_name","description?","expires_in_days?"}`. Creates (and, if
needed, registers) the cluster and returns the token **once**:
`{id, token, cluster_name, token_prefix, expires_at, created_at}`.

### `GET /api/v1/admin/agent-tokens[?cluster=<name>]`
Lists token metadata (never the secret).

### `DELETE /api/v1/admin/agent-tokens/{id}`
Revokes a token (idempotent).

## Admin — users

### `GET /api/v1/admin/users`
Lists users (password hashes are never serialized).

### `POST /api/v1/admin/users`
Body: `{"email","name","password","is_active?"}`. `409` on duplicate email.

### `PUT /api/v1/admin/users/{id}`
Body: any of `{"name","is_active","password"}`. `404` if not found.

### `DELETE /api/v1/admin/users/{id}`
Deletes the user.

## Admin — audit

### `GET /api/v1/admin/audit`
Query params: `user`, `cluster`, `status`, `from`/`to` (RFC3339), `page`,
`per_page` (max 200). Returns `{logs, total, page, per_page}`, newest first.
