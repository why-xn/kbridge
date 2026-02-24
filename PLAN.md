# kbridge - Implementation Plan

## Current Status

Phases 1 and 2 are complete. The core system is functional:
- CLI, Central, and Agent binaries build and work end-to-end
- Agent registration, heartbeat, and command polling implemented
- kubectl passthrough and kubectl edit supported
- E2E tests passing with Kind clusters
- In-memory agent store and command queue

## Phase 3: Authentication

### 3.1 Database Setup

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

### 3.2 User Authentication

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

### 3.3 CLI Login Flow

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

### 3.4 Agent Token Authentication

Secure agent registration with database-backed tokens.

**Tasks:**
- Store hashed agent tokens in database instead of in-memory
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

## Phase 4: RBAC

### 4.1 Role Definitions

Implement the role-based access control model.

**Tasks:**
- Create Role model with JSON permissions:
  ```json
  {
    "clusters": ["dev-*", "staging"],
    "namespaces": ["default", "app-*"],
    "resources": ["pods", "services", "deployments"],
    "verbs": ["get", "list", "logs"]
  }
  ```
- Create `internal/auth/rbac.go` with permission checking logic
- Support wildcard matching for clusters, namespaces, resources
- Create default roles on startup:
  - `admin` - full access to all clusters
  - `developer` - read/write on dev clusters
  - `viewer` - read-only on all clusters
- Add admin API endpoints:
  - `GET /api/v1/admin/roles` - list roles
  - `POST /api/v1/admin/roles` - create role
  - `PUT /api/v1/admin/roles/{id}` - update role
  - `DELETE /api/v1/admin/roles/{id}` - delete role

**Acceptance Criteria:**
- Roles can be created with cluster/namespace/resource permissions
- Wildcard patterns match correctly
- Default roles exist on startup

### 4.2 Permission Enforcement

Enforce RBAC on all kubectl command executions.

**Tasks:**
- Parse kubectl command args to extract:
  - Verb (get, list, create, delete, apply, edit, logs, exec)
  - Resource type (pods, services, deployments, etc.)
  - Namespace (from -n flag or default)
- Check user permissions before routing command to agent
- Return 403 with descriptive message if not permitted
- Add user-role assignment API:
  - `PUT /api/v1/admin/users/{id}/roles` - assign roles
  - `GET /api/v1/admin/users/{id}/roles` - list user roles
- Log permission denials

**Acceptance Criteria:**
- Unauthorized commands return 403
- Authorized commands succeed
- Wildcard patterns work (dev-* matches dev-cluster)
- Permission denials are logged

### 4.3 Admin User Management

Add admin endpoints for managing users.

**Tasks:**
- `GET /api/v1/admin/users` - list all users
- `POST /api/v1/admin/users` - create user
- `PUT /api/v1/admin/users/{id}` - update user
- `DELETE /api/v1/admin/users/{id}` - delete user
- Add admin role check middleware (only admin role can access /admin/ endpoints)
- Add `kbridge admin users list` CLI command
- Add `kbridge admin users create` CLI command

**Acceptance Criteria:**
- Admin can create, list, update, delete users
- Non-admin users cannot access admin endpoints
- CLI admin commands work

---

## Phase 5: Production Readiness

### 5.1 TLS/mTLS

Secure all communication with TLS.

**Tasks:**
- Central: Add TLS config for HTTP server (cert_file, key_file)
- Central: Add TLS config for gRPC server
- Agent: Add TLS config for gRPC client (ca_file, insecure flag)
- CLI: Support HTTPS URLs
- Support self-signed certificates for development
- Add config options:
  ```yaml
  tls:
    enabled: true
    cert_file: /etc/kbridge/tls.crt
    key_file: /etc/kbridge/tls.key
    ca_file: /etc/kbridge/ca.crt
  ```

**Acceptance Criteria:**
- All communication encrypted with TLS
- Invalid certificates rejected
- Insecure mode available for development

### 5.2 Audit Logging

Log all kubectl commands for compliance and debugging.

**Tasks:**
- Create audit_logs table (user, cluster, command, timestamp, duration, status, exit_code)
- Log every command execution in the exec handler
- Add `GET /api/v1/admin/audit` endpoint with filtering (user, cluster, date range)
- Add `kbridge admin audit` CLI command
- Add log retention/cleanup (configurable max age)

**Acceptance Criteria:**
- All commands logged with user, cluster, command, result
- Audit log queryable via API and CLI
- Old logs cleaned up automatically

### 5.3 Docker Images

Containerize all components.

**Tasks:**
- Create multi-stage Dockerfile for CLI
- Create multi-stage Dockerfile for central
- Create multi-stage Dockerfile for agent
- Use Alpine as base for minimal image size
- Add `make docker` target to Makefile
- Push to container registry (configurable)

**Acceptance Criteria:**
- `make docker` builds all images
- Images are minimal (<50MB)
- Images work correctly

### 5.4 Helm Charts

Create Helm charts for Kubernetes deployment.

**Tasks:**
- Create Helm chart for central service:
  - Deployment, Service, ConfigMap, Secret
  - Ingress (optional)
  - PersistentVolumeClaim for SQLite (or external database config)
- Create Helm chart for agent:
  - Deployment, ServiceAccount, ClusterRole, ClusterRoleBinding
  - ConfigMap, Secret for agent token
- Add configurable values.yaml for both charts
- Add documentation

**Acceptance Criteria:**
- `helm install kbridge-central ./charts/central` works
- `helm install kbridge-agent ./charts/agent` works
- All config values are customizable

### 5.5 Documentation

Write comprehensive documentation.

**Tasks:**
- Update README.md with final features
- Write installation guide (binary, Docker, Helm)
- Write configuration reference (all options)
- Write CLI command reference (all commands with examples)
- Write API reference (all endpoints)
- Write admin guide (user management, RBAC setup)
- Add architecture diagrams

**Acceptance Criteria:**
- New users can get started from README
- All config options documented
- All CLI commands documented
- All API endpoints documented

---

## Summary

| Phase | Focus | Status |
|-------|-------|--------|
| Phase 1 | Project setup, skeletons | Done |
| Phase 2 | Core kubectl proxy functionality | Done |
| Phase 3 | Authentication (DB, JWT, login) | Next |
| Phase 4 | RBAC (roles, permissions, admin) | Planned |
| Phase 5 | Production (TLS, audit, Docker, Helm) | Planned |

**Recommended order:** Complete Phase 3 first to secure the system, then Phase 4 for access control, then Phase 5 for deployment readiness.
