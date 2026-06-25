# kbridge

![CI](https://github.com/why-xn/kbridge/actions/workflows/ci.yml/badge.svg)
![Release](https://img.shields.io/github/v/release/why-xn/kbridge)
![Go](https://img.shields.io/github/go-mod/go-version/why-xn/kbridge)
![License](https://img.shields.io/badge/license-Elastic%20License%202.0-blue)

A lightweight, secure CLI tool for managing and accessing multiple Kubernetes clusters through a central gateway — without distributing kubeconfig files, opening inbound firewall rules, or requiring VPN access.

```bash
curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/main/install.sh | sh
```

## Problem

Organizations running multiple Kubernetes clusters face a compounding set of operational and security challenges:

- **Credential sprawl** — Every developer needs kubeconfig files for every cluster they access. Distributing, rotating, and revoking these credentials across teams is error-prone and doesn't scale.
- **No visibility** — There's no centralized record of who ran what command on which cluster. When incidents happen, tracing actions back to individuals requires stitching together logs from multiple sources.
- **Network complexity** — Cluster API servers are typically behind firewalls or private networks. Granting access means configuring VPNs, bastion hosts, or public endpoints — each adding attack surface and operational overhead.
- **Coarse access control** — Kubernetes RBAC is powerful but cluster-scoped. Enforcing consistent policies across many clusters requires duplicating configuration and hoping nothing drifts.

These problems get worse with every new cluster, team, and environment.

## Solution

kbridge eliminates direct cluster access by placing a central gateway between users and clusters. Users interact with a single CLI tool; clusters run a lightweight agent that connects outbound to the gateway. No inbound ports, no kubeconfig distribution, no VPN required.

1. **Central Service (`kbridge-central`)** — API gateway that authenticates users, enforces access policies, queues commands, and collects results. The single point of control for all cluster access.
2. **Cluster Agent (`kbridge-agent`)** — A small daemon deployed in each cluster that initiates an outbound gRPC connection to central. It polls for pending commands, executes them via kubectl locally, and returns results. Since connections are outbound-only, no firewall changes or public endpoints are needed.
3. **CLI (`kb`)** — A user-friendly command-line tool that talks to central over REST. Developers use familiar kubectl syntax (`kb get pods`) without needing direct cluster credentials or network access. (Installed as `kb`, with a `kbridge` symlink for back-compat.)

## Architecture

```
                                    +----------------------------------+
                                    |         Kubernetes Cluster A     |
                                    |  +-----------------------------+ |
                                    |  |     kbridge-agent           | |
+----------------+                  |  |  +---------+  +----------+  | |
|                |                  |  |  |  gRPC   |  | K8s API  |  | |
|  kbridge CLI   |                  |  |  | Client  |--| Client   |  | |
|                |                  |  |  +---------+  +----------+  | |
|  - login       |  +-----------+  |  +-------+---------------------+ |
|  - clusters    |->|           |<-+----------+                       |
|  - use         |  |  Central  |  +----------------------------------+
|  - kubectl     |<-|  Service  |
|                |  |           |  +----------------------------------+
+----------------+  |  - Auth   |  |         Kubernetes Cluster B     |
                    |  - RBAC   |  |  +-----------------------------+ |
                    |  - Proxy  |  |  |     kbridge-agent           | |
                    |  - Audit  |<-+--|  +---------+  +----------+  | |
                    |           |  |  |  |  gRPC   |  | K8s API  |  | |
                    +-----+-----+  |  |  | Client  |--| Client   |  | |
                          |        |  |  +---------+  +----------+  | |
                          v        |  +-------+---------------------+ |
                    +-----------+  |          |                       |
                    | Database  |  +----------+-----------------------+
                    | (SQLite/  |             |
                    |  Postgres)|<------------+
                    +-----------+
```

### How It Works

```
CLI (kbridge) --HTTP REST--> Central Service <--gRPC-- Agent (per cluster) --> kubectl
```

- **CLI to Central**: REST API for login, cluster listing, and kubectl execution
- **Agent to Central**: gRPC for registration, heartbeats, command polling, and result submission
- **Agent to K8s**: kubectl for local command execution

## Components

### CLI (`kb`)

User-facing command-line tool. **kubectl by default** — anything that isn't a
management command (`login`, `logout`, `status`, `clusters`, `admin`) is run as
kubectl on the selected cluster. (`kbridge` remains as a back-compat alias.)

```bash
kb login                      # Login to central service
kb logout                     # Logout
kb clusters list              # List available clusters
kb clusters use <cluster>     # Select active cluster
kb get pods                   # Run kubectl on the selected cluster
kb apply -f app.yaml          # Any kubectl command works
kb logs -f deploy/api         # Follow/watch (-f/-w) streams live until Ctrl-C
kb exec -it deploy/api -- sh  # Interactive shell (full TTY; -i for stdin-only)
kb port-forward deploy/db 5432:5432  # Forward pod port to localhost
kb kubectl get pods           # 'kubectl'/'k' force kubectl explicitly
kb status                     # Show current context

# Admin (requires the admin role)
kb admin users list                          # List users
kb admin users create --email dev@corp.com --name Dev
kb admin agent-tokens create --cluster prod  # Generate an agent token
kb admin audit --user dev@corp.com           # View the command audit log
```

### Central Service (`kbridge-central`)

API gateway and control plane. Handles user authentication, cluster registry, RBAC enforcement, command proxying, and audit logging.

### Agent (`kbridge-agent`)

Lightweight daemon running in each Kubernetes cluster. Connects outbound to central, registers cluster metadata, polls for commands, executes kubectl locally, and submits results back.

## Quick Start

### Prerequisites

- Go 1.25+
- kubectl installed
- Access to a Kubernetes cluster

### Build

```bash
make build
```

This produces three binaries in `bin/`:
- `kb` - CLI tool (with a `kbridge` symlink for back-compat)
- `kbridge-central` - Central service
- `kbridge-agent` - Cluster agent

### Run Locally

**1. Start the central service:**
```bash
# central requires a real jwt_secret (>=32 chars). Generate one:
export KBRIDGE_JWT_SECRET="$(openssl rand -hex 32)"
./bin/kbridge-central --config configs/central.yaml
```

**2. Start an agent (in a cluster with kubectl access):**
```bash
./bin/kbridge-agent --config configs/agent.yaml
```

**3. Log in and use the CLI:**
```bash
# Default admin is seeded from central.yaml (admin@kbridge.local / admin123 in
# the example config — change admin_password after first login for any real deployment).
./bin/kb login
./bin/kb clusters list
./bin/kb clusters use dev-cluster
./bin/kb get pods -A
```

See [docs/installation.md](docs/installation.md) for binary, Docker, and Helm
installs, and the [Documentation](#documentation) section below for full references.

## Configuration

### Central Service (`central.yaml`)

```yaml
server:
  http_port: 8080                  # REST API port
  grpc_port: 9090                  # gRPC server port
database:
  driver: sqlite
  path: kbridge.db
auth:
  jwt_secret: "change-me"          # required
  admin_email: admin@kbridge.local # seeded on first start
  admin_password: changeme
rbac:
  policy_file: configs/rbac.yaml   # empty = enforcement disabled
tls:
  enabled: false                   # see docs/configuration.md#tls
```

See [docs/configuration.md](docs/configuration.md) for the complete reference
(audit, bootstrap, TLS, env vars).

### Agent (`agent.yaml`)

```yaml
central:
  url: localhost:9090              # Central gRPC address
  token: dev-token                 # Authentication token

cluster:
  name: dev-cluster                # Unique cluster identifier
```

### CLI (`~/.kbridge/config.yaml`)

```yaml
central_url: https://central.example.com:8080
current_cluster: production-us-east
token: ""
```

### Environment Variables

**Agent:**

| Variable | Description | Default |
|----------|-------------|---------|
| `KBRIDGE_CONFIG` | Path to config file | `configs/agent.yaml` or `/etc/kbridge/agent.yaml` |
| `KBRIDGE_CENTRAL_URL` | Central gRPC address | `localhost:9090` |
| `KBRIDGE_AGENT_TOKEN` | Authentication token | — |
| `KBRIDGE_CLUSTER_NAME` | Cluster name | `default` |

**Central:**

| Variable | Description | Default |
|----------|-------------|---------|
| `KBRIDGE_CONFIG` | Path to config file | `configs/central.yaml` |

## Agent Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kbridge-agent
  namespace: kbridge-system
spec:
  replicas: 1
  template:
    spec:
      serviceAccountName: kbridge-agent
      containers:
      - name: agent
        image: kbridge-agent:latest
        env:
        - name: KBRIDGE_CENTRAL_URL
          value: "central.example.com:9090"
        - name: KBRIDGE_CLUSTER_NAME
          value: "production-us-east"
        - name: KBRIDGE_AGENT_TOKEN
          valueFrom:
            secretKeyRef:
              name: kbridge-agent
              key: token
```

## Security Model

### Authentication

Users authenticate via `kb login`, which obtains a JWT token from central and stores it locally. All subsequent API calls include this token.

### RBAC

Access control is defined in a declarative, hot-reloaded policy file
(ArgoCD-style), enforced on every kubectl command before it reaches a cluster.
Roles match by cluster, namespace, resource, and verb with wildcards; bindings
map users (by JWT email) to roles:

```yaml
default: viewer
roles:
  - name: developer
    rules:
      - clusters: ["dev-*", "staging"]
        namespaces: ["*"]
        resources: ["pods", "services", "deployments"]
        verbs: ["get", "list", "logs", "exec", "apply"]
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
bindings:
  - subject: admin@kbridge.local
    roles: ["admin"]
```

See [docs/rbac.md](docs/rbac.md) for the full policy reference.

### Agent Authentication

Agents authenticate with database-backed tokens. Each token is a high-entropy
random secret, shown once at creation and stored only as an
HMAC-SHA256 digest keyed by a server-side pepper (`auth.token_pepper`, falling
back to `jwt_secret`) — so a stolen database alone cannot be used to verify
guessed tokens. Each token is bound to one cluster, can be revoked via the admin
API, and records a `last_used_at` timestamp on every successful registration for
staleness detection. Tokens are managed with
`POST/GET/DELETE /api/v1/admin/agent-tokens`.

### TLS

Server-authenticated TLS secures the HTTP API, the agent↔central gRPC channel,
and the CLI. Generate a dev certificate with `make certs`; see
[docs/configuration.md](docs/configuration.md#tls).

### Audit Logging

Every command (allowed, denied, failed, or timed out) is recorded with user,
cluster, command, result, and duration. Query via `kb admin audit` or
`GET /api/v1/admin/audit`.

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go |
| CLI | Cobra + Viper |
| HTTP Server | Gin |
| RPC | gRPC + Protocol Buffers |
| Database | SQLite (`modernc.org/sqlite`, pure Go) |
| Auth | JWT (HS256) + bcrypt |
| Authorization | Declarative policy file (hot-reloaded) |
| Transport security | Server-authenticated TLS (HTTP + gRPC) |
| Config | YAML + environment variables |

## Documentation

| Guide | Contents |
|-------|----------|
| [Installation](docs/installation.md) | Binary, Docker, and Helm installation |
| [Configuration](docs/configuration.md) | All central / agent / CLI options, incl. TLS |
| [CLI reference](docs/cli.md) | Every command with examples |
| [API reference](docs/api.md) | All HTTP endpoints |
| [RBAC](docs/rbac.md) | Policy file format and examples |
| [Admin guide](docs/admin.md) | Users, agent tokens, and audit logs |

## License

[Elastic License 2.0 (ELv2)](LICENSE) — free to use and modify. Commercial distribution and offering as a hosted/managed service are not permitted.
