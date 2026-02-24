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
| POST | `/auth/login` | User login (planned) |
| POST | `/auth/refresh` | Refresh token (planned) |

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

Agents use the polling-based flow: `GetPendingCommands` + `SubmitCommandResult`. The `ExecuteCommand` streaming RPC is defined but not yet implemented.

## Data Models (Planned)

```sql
CREATE TABLE users (
    id          UUID PRIMARY KEY,
    email       VARCHAR(255) UNIQUE NOT NULL,
    password    VARCHAR(255) NOT NULL,
    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE clusters (
    id          UUID PRIMARY KEY,
    name        VARCHAR(255) UNIQUE NOT NULL,
    agent_token VARCHAR(255) NOT NULL,
    last_seen   TIMESTAMP,
    status      VARCHAR(50) DEFAULT 'disconnected',
    metadata    JSONB,
    created_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE roles (
    id          UUID PRIMARY KEY,
    name        VARCHAR(255) UNIQUE NOT NULL,
    permissions JSONB NOT NULL,
    created_at  TIMESTAMP DEFAULT NOW()
);

CREATE TABLE user_roles (
    user_id     UUID REFERENCES users(id),
    role_id     UUID REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY,
    user_id     UUID REFERENCES users(id),
    cluster_id  UUID REFERENCES clusters(id),
    command     TEXT NOT NULL,
    status      VARCHAR(50),
    duration_ms INTEGER,
    created_at  TIMESTAMP DEFAULT NOW()
);
```
