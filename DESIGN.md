# mk8s - Multi-Cluster Kubernetes CLI

A lightweight CLI tool for managing multiple Kubernetes clusters through a central gateway, without requiring direct cluster access.

## Problem Statement

Managing multiple Kubernetes clusters presents several challenges:
- Users need kubeconfig files with direct cluster access
- Distributing and rotating credentials is complex
- No centralized audit trail of who ran what commands
- Difficult to enforce fine-grained access control
- VPN or direct network access often required

## Solution

mk8s provides a secure, centralized way to access multiple Kubernetes clusters:

1. **Central Service** - API gateway that manages authentication and routes commands
2. **Cluster Agents** - Lightweight agents running in each cluster, connecting to central
3. **CLI Tool** - User-friendly CLI that communicates with central service

## Architecture

```
                                    ┌──────────────────────────────────┐
                                    │         Kubernetes Cluster A     │
                                    │  ┌─────────────────────────────┐ │
                                    │  │     mk8s-agent              │ │
┌──────────────┐                    │  │  ┌─────────┐  ┌──────────┐  │ │
│              │                    │  │  │  gRPC   │──│ K8s API  │  │ │
│   mk8s CLI   │                    │  │  │ Client  │  │  Client  │  │ │
│              │                    │  │  └─────────┘  └──────────┘  │ │
│  - login     │    ┌───────────┐   │  └───────┬─────────────────────┘ │
│  - clusters  │───▶│           │◀──┼──────────┘                       │
│  - use       │    │  Central  │   └──────────────────────────────────┘
│  - kubectl   │◀───│  Service  │
│              │    │           │   ┌──────────────────────────────────┐
└──────────────┘    │  - Auth   │   │         Kubernetes Cluster B     │
                    │  - RBAC   │   │  ┌─────────────────────────────┐ │
                    │  - Proxy  │   │  │     mk8s-agent              │ │
                    │  - Audit  │◀──┼──│  ┌─────────┐  ┌──────────┐  │ │
                    │           │   │  │  │  gRPC   │──│ K8s API  │  │ │
                    └─────┬─────┘   │  │  │ Client  │  │  Client  │  │ │
                          │         │  │  └─────────┘  └──────────┘  │ │
                          ▼         │  └───────┬─────────────────────┘ │
                    ┌───────────┐   │          │                       │
                    │ Database  │   └──────────┼───────────────────────┘
                    │ (SQLite/  │              │
                    │  Postgres)│◀─────────────┘
                    └───────────┘
```

## Components

### 1. CLI (`mk8s`)

User-facing command-line tool.

```bash
# Authentication
mk8s login                      # Login to central service
mk8s logout                     # Logout

# Cluster management
mk8s clusters list              # List available clusters
mk8s clusters use <cluster>     # Select active cluster

# Kubectl proxy
mk8s kubectl get pods           # Run kubectl on selected cluster
mk8s kubectl apply -f app.yaml  # Any kubectl command works

# Shorthand
mk8s k get pods                 # 'k' alias for kubectl

# Context info
mk8s status                     # Show current user, cluster, permissions
```

### 2. Central Service (`mk8s-central`)

API gateway and control plane.

**Responsibilities:**
- User authentication (JWT tokens)
- Cluster registry (which agents are connected)
- RBAC enforcement (who can access what)
- Command proxying (route kubectl to correct agent)
- Audit logging (record all commands)

**API Endpoints:**
```
POST   /auth/login          # User login
POST   /auth/refresh        # Refresh token
GET    /clusters            # List clusters user can access
POST   /clusters/{id}/exec  # Execute kubectl command
GET    /users               # Admin: list users
POST   /users               # Admin: create user
PUT    /users/{id}/roles    # Admin: assign roles
```

**gRPC Service (for agents):**
```protobuf
service AgentService {
  rpc Register(RegisterRequest) returns (RegisterResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc ExecuteCommand(CommandRequest) returns (CommandResponse);
}
```

### 3. Agent (`mk8s-agent`)

Runs in each Kubernetes cluster.

**Responsibilities:**
- Connect to central service (outbound connection)
- Register cluster metadata
- Execute kubectl commands locally
- Stream results back to central

**Deployment:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mk8s-agent
  namespace: mk8s-system
spec:
  replicas: 1
  template:
    spec:
      serviceAccountName: mk8s-agent
      containers:
      - name: agent
        image: mk8s-agent:latest
        env:
        - name: CENTRAL_URL
          value: "https://central.example.com"
        - name: CLUSTER_NAME
          value: "production-us-east"
        - name: AGENT_TOKEN
          valueFrom:
            secretKeyRef:
              name: mk8s-agent
              key: token
```

## Security Model

### Authentication Flow

```
┌────────┐     ┌─────────┐     ┌─────────────┐
│  User  │────▶│   CLI   │────▶│   Central   │
└────────┘     └─────────┘     └─────────────┘
    │                               │
    │ 1. mk8s login                 │
    │──────────────────────────────▶│
    │                               │
    │ 2. Enter credentials          │
    │◀──────────────────────────────│
    │                               │
    │ 3. Return JWT token           │
    │◀──────────────────────────────│
    │                               │
    │ 4. Store token locally        │
    │                               │
```

### RBAC Model

```yaml
# Role definition
roles:
  - name: developer
    clusters:
      - name: "dev-*"           # Wildcard matching
        namespaces: ["*"]       # All namespaces
        verbs: ["get", "list", "logs", "exec"]
        resources: ["pods", "services", "deployments"]

  - name: viewer
    clusters:
      - name: "*"
        namespaces: ["*"]
        verbs: ["get", "list"]
        resources: ["*"]

  - name: admin
    clusters:
      - name: "*"
        namespaces: ["*"]
        verbs: ["*"]
        resources: ["*"]

# User assignment
users:
  - email: "dev@example.com"
    roles: ["developer"]
  - email: "ops@example.com"
    roles: ["admin"]
```

### Agent Authentication

Agents use pre-shared tokens + mTLS:

1. Admin generates agent token in central
2. Token deployed as K8s secret
3. Agent uses token for initial auth
4. Central issues short-lived certificates
5. All communication over mTLS

## Data Models

### Database Schema

```sql
-- Users
CREATE TABLE users (
    id          UUID PRIMARY KEY,
    email       VARCHAR(255) UNIQUE NOT NULL,
    password    VARCHAR(255) NOT NULL,  -- bcrypt hash
    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW()
);

-- Clusters (registered agents)
CREATE TABLE clusters (
    id          UUID PRIMARY KEY,
    name        VARCHAR(255) UNIQUE NOT NULL,
    agent_token VARCHAR(255) NOT NULL,
    last_seen   TIMESTAMP,
    status      VARCHAR(50) DEFAULT 'disconnected',
    metadata    JSONB,
    created_at  TIMESTAMP DEFAULT NOW()
);

-- Roles
CREATE TABLE roles (
    id          UUID PRIMARY KEY,
    name        VARCHAR(255) UNIQUE NOT NULL,
    permissions JSONB NOT NULL,
    created_at  TIMESTAMP DEFAULT NOW()
);

-- User-Role mapping
CREATE TABLE user_roles (
    user_id     UUID REFERENCES users(id),
    role_id     UUID REFERENCES roles(id),
    PRIMARY KEY (user_id, role_id)
);

-- Audit log
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

## Tech Stack

| Component | Technology |
|-----------|------------|
| CLI | Go + Cobra + Viper |
| Central Service | Go + Gin/Echo + gRPC |
| Agent | Go + gRPC + client-go |
| Database | SQLite (dev) / PostgreSQL (prod) |
| Auth | JWT (RS256) |
| Transport | gRPC + TLS |
| Config | YAML files |

## Project Structure

```
mk8s/
├── cmd/
│   ├── mk8s/              # CLI entrypoint
│   │   └── main.go
│   ├── central/           # Central service entrypoint
│   │   └── main.go
│   └── agent/             # Agent entrypoint
│       └── main.go
├── internal/
│   ├── cli/               # CLI commands
│   │   ├── root.go
│   │   ├── login.go
│   │   ├── clusters.go
│   │   └── kubectl.go
│   ├── central/           # Central service
│   │   ├── server.go
│   │   ├── auth.go
│   │   ├── handlers.go
│   │   └── grpc.go
│   ├── agent/             # Agent
│   │   ├── agent.go
│   │   ├── executor.go
│   │   └── client.go
│   ├── auth/              # Shared auth utilities
│   │   ├── jwt.go
│   │   └── rbac.go
│   └── models/            # Shared data models
│       ├── user.go
│       ├── cluster.go
│       └── role.go
├── api/
│   └── proto/             # gRPC definitions
│       └── agent.proto
├── deploy/
│   ├── helm/              # Helm charts
│   │   ├── central/
│   │   └── agent/
│   └── docker/            # Dockerfiles
│       ├── Dockerfile.central
│       └── Dockerfile.agent
├── configs/
│   ├── central.yaml       # Central config example
│   └── agent.yaml         # Agent config example
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

## Development Phases

### Phase 1: Foundation
- [ ] Project structure setup
- [ ] CLI skeleton with Cobra
- [ ] Basic gRPC proto definitions
- [ ] Central service skeleton
- [ ] Agent skeleton

### Phase 2: Core Functionality
- [ ] Agent registration with central
- [ ] Agent heartbeat mechanism
- [ ] Basic kubectl proxy (get pods)
- [ ] CLI cluster listing
- [ ] CLI kubectl passthrough

### Phase 3: Authentication
- [ ] User model and database
- [ ] JWT token generation/validation
- [ ] CLI login/logout
- [ ] Protected API endpoints
- [ ] Agent token authentication

### Phase 4: RBAC
- [ ] Role definitions
- [ ] Permission checking
- [ ] User-role assignment
- [ ] Namespace filtering
- [ ] Command filtering

### Phase 5: Production Readiness
- [ ] TLS/mTLS setup
- [ ] Audit logging
- [ ] Helm charts
- [ ] Docker images
- [ ] Documentation

## Configuration Examples

### Central Service (`central.yaml`)

```yaml
server:
  http_port: 8080
  grpc_port: 9090

database:
  driver: postgres
  url: postgres://user:pass@localhost/mk8s

auth:
  jwt_secret: "your-secret-key"
  token_expiry: 24h

tls:
  enabled: true
  cert_file: /etc/mk8s/tls.crt
  key_file: /etc/mk8s/tls.key
```

### Agent (`agent.yaml`)

```yaml
central:
  url: https://central.example.com:9090
  token: ${AGENT_TOKEN}

cluster:
  name: production-us-east

tls:
  insecure: false
  ca_file: /etc/mk8s/ca.crt
```

### CLI (`~/.mk8s/config.yaml`)

```yaml
central_url: https://central.example.com:8080
current_cluster: production-us-east
```

## Agent Development Guidelines

**IMPORTANT: These guidelines are for the AI agent working on this project. Follow them for every task.**

### Ownership Mentality

You are the primary developer of this project. Act with full ownership:

1. **Think holistically** - Consider how each change affects the entire system
2. **Maintain quality** - Never compromise on code quality to finish faster
3. **Be proactive** - If you notice issues while working on a task, fix them
4. **Document decisions** - Add comments explaining non-obvious design choices

### Code Quality Standards

#### Testing Requirements

- **Minimum 70% test coverage** for all new code
- Write tests BEFORE or ALONGSIDE implementation (TDD encouraged)
- Every public function must have at least one test
- Test both success and error cases
- Use table-driven tests for multiple scenarios:

```go
func TestParseConfig(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    *Config
        wantErr bool
    }{
        {"valid config", "...", &Config{...}, false},
        {"missing field", "...", nil, true},
        {"invalid yaml", "...", nil, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // test implementation
        })
    }
}
```

#### Function Design

- **Keep functions small** - Each function should do ONE thing
- **Maximum 30 lines** per function (excluding comments)
- **Maximum 3 levels** of nesting
- If a function is getting long, extract helper functions
- This improves testability and readability

```go
// BAD - One large function
func ProcessRequest(req *Request) (*Response, error) {
    // 100 lines of code doing validation, processing, formatting...
}

// GOOD - Small, focused functions
func ProcessRequest(req *Request) (*Response, error) {
    if err := validateRequest(req); err != nil {
        return nil, err
    }
    result, err := executeRequest(req)
    if err != nil {
        return nil, err
    }
    return formatResponse(result), nil
}

func validateRequest(req *Request) error { ... }
func executeRequest(req *Request) (*Result, error) { ... }
func formatResponse(result *Result) *Response { ... }
```

#### Error Handling

- Always wrap errors with context: `fmt.Errorf("failed to connect: %w", err)`
- Define custom error types for domain errors
- Never ignore errors (use `_ = ` only if truly intentional)

### Documentation Requirements

#### README.md Updates

Update README.md whenever you:
- Add new CLI commands or flags
- Add new configuration options
- Change installation steps
- Add new dependencies
- Change build or run instructions

README.md must always include:
1. **Quick Start** - Get running in under 2 minutes
2. **Installation** - All installation methods
3. **Configuration** - All config options with examples
4. **Usage** - Common use cases with examples
5. **Development** - How to build and test locally

#### Code Comments

- Add comments for **why**, not **what**
- Document all public functions with GoDoc format
- Add package-level documentation in `doc.go` files

```go
// Package auth provides authentication and authorization
// utilities for the mk8s system, including JWT token
// generation, validation, and RBAC enforcement.
package auth

// ValidateToken checks if the provided JWT token is valid
// and not expired. It returns the claims if valid, or an
// error describing why validation failed.
func ValidateToken(token string) (*Claims, error) {
    // ... implementation
}
```

### Git Commit Guidelines

- Write clear, descriptive commit messages
- Use conventional commits format:
  - `feat:` - New feature
  - `fix:` - Bug fix
  - `test:` - Adding tests
  - `docs:` - Documentation
  - `refactor:` - Code refactoring
  - `chore:` - Maintenance tasks

### Before Completing Any Task

Always verify:

1. **Code compiles**: `go build ./...`
2. **Tests pass**: `go test ./...`
3. **No lint errors**: `go vet ./...`
4. **Documentation updated**: README, comments, GoDoc
5. **No hardcoded values**: Use config or constants

### Project-Specific Rules

1. **All HTTP handlers** must log request/response
2. **All gRPC methods** must have timeout handling
3. **All external calls** must have retry logic
4. **All config values** must have sensible defaults
5. **All CLI commands** must have `--help` documentation

### When Stuck or Uncertain

1. Re-read this DESIGN.md for architecture guidance
2. Check existing code for patterns to follow
3. Prefer simple solutions over clever ones
4. If multiple approaches exist, choose the most maintainable

## API Examples

### Login
```bash
curl -X POST https://central.example.com/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "user@example.com", "password": "secret"}'

# Response
{
  "token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_at": "2024-01-02T00:00:00Z"
}
```

### List Clusters
```bash
curl https://central.example.com/clusters \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiIs..."

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
curl -X POST https://central.example.com/clusters/production-us-east/exec \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiIs..." \
  -H "Content-Type: application/json" \
  -d '{"command": ["get", "pods", "-n", "default"]}'

# Response
{
  "output": "NAME                     READY   STATUS    RESTARTS   AGE\nnginx-7c5b4f6b8d-x2j4k   1/1     Running   0          2d",
  "exit_code": 0
}
```
