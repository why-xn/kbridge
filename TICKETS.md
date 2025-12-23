# mk8s - Implementation Tickets

Tickets are organized by phase and priority. Each ticket should be created as a GitHub issue with the `agent-task` label.

---

## Phase 1: Foundation

### Ticket 1.1: Initialize Go Project Structure
**Priority:** High
**Labels:** `agent-task`, `type:coding`, `priority:high`

**Description:**
Set up the Go project structure for mk8s with proper module organization.

**Tasks:**
- Initialize Go module (`go mod init github.com/why-xn/mk8s`)
- Create directory structure:
  ```
  cmd/mk8s/
  cmd/central/
  cmd/agent/
  internal/cli/
  internal/central/
  internal/agent/
  internal/models/
  api/proto/
  ```
- Create placeholder main.go files for each cmd
- Create Makefile with build targets
- Add .gitignore for Go projects

**Acceptance Criteria:**
- `go build ./...` succeeds
- `make build` builds all three binaries

---

### Ticket 1.2: CLI Skeleton with Cobra
**Priority:** High
**Labels:** `agent-task`, `type:coding`, `priority:high`

**Description:**
Create the basic CLI structure using Cobra.

**Tasks:**
- Add Cobra and Viper dependencies
- Create root command with version flag
- Create subcommands:
  - `mk8s login`
  - `mk8s logout`
  - `mk8s clusters list`
  - `mk8s clusters use <name>`
  - `mk8s kubectl <args...>`
  - `mk8s status`
- Add help text for all commands
- Set up Viper for config file (~/.mk8s/config.yaml)

**Acceptance Criteria:**
- `mk8s --help` shows all commands
- `mk8s version` shows version info
- Commands exist (can show "not implemented" for now)

---

### Ticket 1.3: Define gRPC Protobuf Schemas
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Define the gRPC service definitions for agent-central communication.

**Tasks:**
- Create `api/proto/agent.proto` with:
  ```protobuf
  service AgentService {
    rpc Register(RegisterRequest) returns (RegisterResponse);
    rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
    rpc ExecuteCommand(stream CommandRequest) returns (stream CommandResponse);
  }
  ```
- Define message types for all requests/responses
- Add buf.yaml or use protoc directly
- Generate Go code from proto files
- Add proto generation to Makefile

**Acceptance Criteria:**
- Proto files compile without errors
- Generated Go code in `api/proto/` or `internal/proto/`

---

### Ticket 1.4: Central Service Skeleton
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Create the basic central service with HTTP and gRPC servers.

**Tasks:**
- Add Gin (or Echo) for HTTP server
- Add gRPC server
- Create config loading from YAML
- Implement basic endpoints:
  - `GET /health` - health check
  - `GET /clusters` - placeholder
- Implement gRPC service (placeholder implementations)
- Add graceful shutdown handling

**Acceptance Criteria:**
- Central starts and listens on configured ports
- Health endpoint returns 200
- gRPC service accepts connections

---

### Ticket 1.5: Agent Skeleton
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Create the basic agent that connects to central service.

**Tasks:**
- Create agent main with config loading
- Implement gRPC client connection to central
- Implement Register RPC call on startup
- Implement Heartbeat loop (every 30s)
- Add reconnection logic on disconnect
- Add graceful shutdown

**Acceptance Criteria:**
- Agent starts and connects to central
- Agent registers itself with cluster name
- Heartbeat keeps connection alive

---

## Phase 2: Core Functionality

### Ticket 2.1: Agent Registration Flow
**Priority:** High
**Labels:** `agent-task`, `type:coding`, `priority:high`

**Description:**
Implement full agent registration with central service.

**Tasks:**
- Central: Store registered agents in memory (map)
- Central: Handle Register RPC - validate token, store agent info
- Central: Handle Heartbeat RPC - update last_seen timestamp
- Central: Track agent status (connected/disconnected)
- Central: Detect disconnected agents (no heartbeat for 60s)
- Agent: Send cluster metadata (name, version, node count)

**Acceptance Criteria:**
- Agent registers successfully with valid token
- Central tracks agent status
- Disconnected agents marked as offline

---

### Ticket 2.2: Basic kubectl Proxy
**Priority:** High
**Labels:** `agent-task`, `type:coding`, `priority:high`

**Description:**
Implement kubectl command execution through the agent.

**Tasks:**
- Agent: Implement ExecuteCommand RPC
- Agent: Use client-go to execute kubectl commands
- Agent: Stream stdout/stderr back to central
- Central: Implement `/clusters/{name}/exec` HTTP endpoint
- Central: Route exec request to correct agent via gRPC
- Central: Stream response back to HTTP client

**Acceptance Criteria:**
- `curl /clusters/dev/exec -d '{"command":["get","pods"]}'` works
- Output is streamed back
- Exit code is returned

---

### Ticket 2.3: CLI Cluster Commands
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Implement cluster listing and selection in CLI.

**Tasks:**
- CLI: Implement `mk8s clusters list` - call GET /clusters
- CLI: Display clusters in table format with status
- CLI: Implement `mk8s clusters use <name>` - save to config
- CLI: Implement `mk8s status` - show current cluster
- CLI: Store current cluster in ~/.mk8s/config.yaml

**Acceptance Criteria:**
- `mk8s clusters list` shows connected clusters
- `mk8s clusters use dev` sets current cluster
- `mk8s status` shows current cluster

---

### Ticket 2.4: CLI kubectl Passthrough
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Implement kubectl command passthrough in CLI.

**Tasks:**
- CLI: Implement `mk8s kubectl <args>` command
- CLI: Read current cluster from config
- CLI: Send command to central service
- CLI: Stream output to terminal
- CLI: Handle exit codes properly
- CLI: Add `mk8s k` as alias for `mk8s kubectl`

**Acceptance Criteria:**
- `mk8s kubectl get pods` works
- Output streams in real-time
- Exit code matches actual kubectl exit code

---

## Phase 3: Authentication

### Ticket 3.1: Database Setup
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Add database support to central service.

**Tasks:**
- Add GORM or sqlx dependency
- Create database models (User, Cluster, Role)
- Implement SQLite driver for development
- Implement PostgreSQL driver for production
- Add database migrations
- Add config options for database

**Acceptance Criteria:**
- Central connects to SQLite in dev mode
- Tables are created on startup
- Database driver is configurable

---

### Ticket 3.2: User Authentication
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Implement user authentication with JWT.

**Tasks:**
- Implement password hashing (bcrypt)
- Implement JWT token generation (RS256)
- Create POST /auth/login endpoint
- Create POST /auth/refresh endpoint
- Add JWT validation middleware
- Protect /clusters endpoints with auth middleware

**Acceptance Criteria:**
- Login returns JWT token
- Protected endpoints require valid token
- Invalid/expired tokens are rejected

---

### Ticket 3.3: CLI Login Flow
**Priority:** High
**Labels:** `agent-task`, `type:coding`

**Description:**
Implement login/logout in CLI.

**Tasks:**
- CLI: Implement `mk8s login` - prompt for email/password
- CLI: Call POST /auth/login
- CLI: Store token in ~/.mk8s/config.yaml
- CLI: Implement `mk8s logout` - remove token
- CLI: Auto-refresh token before expiry
- CLI: Add token to all API requests

**Acceptance Criteria:**
- `mk8s login` prompts and stores token
- `mk8s logout` removes token
- API calls include Authorization header

---

### Ticket 3.4: Agent Token Authentication
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Secure agent registration with tokens.

**Tasks:**
- Central: Generate agent tokens (CLI or API)
- Central: Store hashed tokens in database
- Central: Validate token on agent Register
- Agent: Read token from config/secret
- Central: Add admin endpoint to manage agent tokens

**Acceptance Criteria:**
- Agent registration requires valid token
- Invalid tokens are rejected
- Admin can generate new tokens

---

## Phase 4: RBAC

### Ticket 4.1: Role Model
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Implement role-based access control model.

**Tasks:**
- Create Role model with permissions JSON
- Create UserRole join table
- Define permission structure:
  ```json
  {
    "clusters": ["dev-*", "staging"],
    "namespaces": ["default", "app-*"],
    "resources": ["pods", "services"],
    "verbs": ["get", "list", "logs"]
  }
  ```
- Add default roles (admin, developer, viewer)

**Acceptance Criteria:**
- Roles can be created with permissions
- Users can be assigned roles
- Default roles exist

---

### Ticket 4.2: Permission Checking
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Enforce RBAC on kubectl commands.

**Tasks:**
- Parse kubectl command to extract:
  - Resource type (pods, services, etc.)
  - Verb (get, list, create, delete)
  - Namespace
- Check user permissions against command
- Return 403 if not permitted
- Support wildcard matching (dev-*)

**Acceptance Criteria:**
- Unauthorized commands are rejected
- Authorized commands succeed
- Wildcards work correctly

---

### Ticket 4.3: Admin User Management
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Add admin endpoints for user management.

**Tasks:**
- Create GET /admin/users - list users
- Create POST /admin/users - create user
- Create PUT /admin/users/{id}/roles - assign roles
- Create DELETE /admin/users/{id} - delete user
- Add admin role check middleware

**Acceptance Criteria:**
- Admin can manage users via API
- Non-admin cannot access admin endpoints

---

## Phase 5: Production Readiness

### Ticket 5.1: TLS/mTLS Setup
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Add TLS support for secure communication.

**Tasks:**
- Central: Add TLS config for HTTP server
- Central: Add TLS config for gRPC server
- Agent: Add TLS config for gRPC client
- Agent: Support CA certificate validation
- CLI: Add TLS config

**Acceptance Criteria:**
- All communication is encrypted
- Invalid certificates are rejected

---

### Ticket 5.2: Audit Logging
**Priority:** Medium
**Labels:** `agent-task`, `type:coding`

**Description:**
Log all kubectl commands for audit.

**Tasks:**
- Create audit_logs table
- Log: user, cluster, command, timestamp, duration, status
- Add GET /admin/audit endpoint
- Add CLI command `mk8s audit` for admins

**Acceptance Criteria:**
- All commands are logged
- Audit log is queryable

---

### Ticket 5.3: Helm Charts
**Priority:** Low
**Labels:** `agent-task`, `type:coding`

**Description:**
Create Helm charts for deployment.

**Tasks:**
- Create Helm chart for central service
- Create Helm chart for agent
- Add configurable values
- Add RBAC/ServiceAccount for agent
- Add documentation

**Acceptance Criteria:**
- `helm install mk8s-central ./charts/central` works
- `helm install mk8s-agent ./charts/agent` works

---

### Ticket 5.4: Docker Images
**Priority:** Low
**Labels:** `agent-task`, `type:coding`

**Description:**
Create Docker images for all components.

**Tasks:**
- Create Dockerfile for CLI
- Create Dockerfile for central
- Create Dockerfile for agent
- Use multi-stage builds
- Add to Makefile

**Acceptance Criteria:**
- `make docker` builds all images
- Images are minimal (Alpine-based)

---

### Ticket 5.5: Documentation
**Priority:** Low
**Labels:** `agent-task`, `type:docs`

**Description:**
Write user and developer documentation.

**Tasks:**
- Update README.md with quick start
- Write installation guide
- Write configuration reference
- Write CLI command reference
- Write API reference
- Add architecture diagrams

**Acceptance Criteria:**
- New users can get started from README
- All config options documented
- All CLI commands documented

---

## Summary

| Phase | Tickets | Focus |
|-------|---------|-------|
| Phase 1 | 1.1 - 1.5 | Project setup, skeletons |
| Phase 2 | 2.1 - 2.4 | Core kubectl proxy functionality |
| Phase 3 | 3.1 - 3.4 | Authentication |
| Phase 4 | 4.1 - 4.3 | RBAC |
| Phase 5 | 5.1 - 5.5 | Production readiness |

**Recommended order:** Complete Phase 1 and 2 first to have a working prototype, then add auth and RBAC.
