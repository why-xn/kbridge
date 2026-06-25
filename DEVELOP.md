# Development Guide

## Project Structure

```
kbridge/
├── cmd/
│   ├── kbridge/           # CLI entrypoint
│   ├── central/           # Central service entrypoint
│   └── agent/             # Agent entrypoint
├── internal/
│   ├── cli/               # CLI commands and HTTP client
│   ├── central/           # Central service (HTTP, gRPC, store, commands)
│   └── agent/             # Agent (connection, heartbeat, executor)
├── api/
│   └── proto/             # gRPC protobuf definitions
│       ├── agent.proto
│       └── agentpb/       # Generated Go code
├── configs/               # Example configuration files
├── scripts/               # Build and test scripts
├── tests/
│   └── e2e/               # End-to-end tests (Kind)
├── Makefile
├── go.mod
└── go.sum
```

## Build

```bash
make build          # Build all binaries
make build-cli      # Build CLI only
make build-central  # Build central only
make build-agent    # Build agent only
make clean          # Remove bin/ directory
```

## Test

```bash
make test           # Run unit tests with coverage
make test-e2e       # Run E2E tests with Kind
make e2e-setup      # Setup Kind cluster for manual testing
make e2e-teardown   # Tear down Kind cluster
```

## Proto Generation

```bash
make proto          # Regenerate Go code from .proto files
```

Requires `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`.

## HTTP API

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/api/v1/clusters` | List registered clusters |
| POST | `/api/v1/clusters/{name}/exec` | Execute kubectl command |
| POST | `/auth/login` | User login |
| POST | `/auth/refresh` | Refresh token |

### List Clusters

```bash
curl http://localhost:8080/api/v1/clusters

# Response
{
  "clusters": [
    {"name": "production-us-east", "status": "connected"},
    {"name": "staging", "status": "connected"},
    {"name": "dev", "status": "disconnected"}
  ]
}
```

### Execute Command

```bash
curl -X POST http://localhost:8080/api/v1/clusters/production-us-east/exec \
  -H "Content-Type: application/json" \
  -d '{"command": ["get", "pods", "-n", "default"]}'

# Response
{
  "output": "NAME                     READY   STATUS    RESTARTS   AGE\nnginx-7c5b4f6b8d-x2j4k   1/1     Running   0          2d",
  "exit_code": 0
}
```

## gRPC Service

Defined in `api/proto/agent.proto`. Used for agent-central communication.

```protobuf
service AgentService {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc ExecuteCommand(CommandRequest) returns (stream CommandResponse);
  rpc GetPendingCommands(GetPendingCommandsRequest) returns (GetPendingCommandsResponse);
  rpc SubmitCommandResult(SubmitCommandResultRequest) returns (SubmitCommandResultResponse);
}
```

Agents use the polling-based flow: `GetPendingCommands` + `SubmitCommandResult`. `OpenStream` supports bidirectional streaming for `logs -f`, `get -w`, interactive `exec`, and `port-forward` sessions.

## Data Models

SQLite schema — managed by `internal/central/migrations.go` and auto-applied on
startup. All IDs are random UUIDs stored as TEXT; timestamps are RFC3339 strings.
RBAC roles are defined in the policy file, not the database.

```sql
CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL,
    is_active     INTEGER NOT NULL DEFAULT 1,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE clusters (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'disconnected',
    agent_id     TEXT,
    last_seen_at TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE agent_tokens (
    id           TEXT PRIMARY KEY,
    cluster_id   TEXT NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL,
    token_prefix TEXT NOT NULL,
    description  TEXT,
    is_revoked   INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT,
    expires_at   TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE audit_logs (
    id            TEXT PRIMARY KEY,
    user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
    user_email    TEXT NOT NULL,
    cluster_name  TEXT NOT NULL,
    cluster_id    TEXT REFERENCES clusters(id) ON DELETE SET NULL,
    command       TEXT NOT NULL,
    namespace     TEXT,
    status        TEXT NOT NULL,
    exit_code     INTEGER,
    duration_ms   INTEGER,
    error_message TEXT,
    client_ip     TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE refresh_tokens (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
```

Note: the old `roles`, `permissions`, and `user_roles` tables are dropped on
migration. RBAC is enforced from the policy file (`rbac.policy_file`), not the DB.
