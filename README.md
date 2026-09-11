# kbridge

![CI](https://github.com/why-xn/kbridge/actions/workflows/ci.yml/badge.svg)
![Release](https://img.shields.io/github/v/release/why-xn/kbridge?include_prereleases)
![Go](https://img.shields.io/github/go-mod/go-version/why-xn/kbridge)
![License](https://img.shields.io/badge/license-Elastic%20License%202.0-blue)

> **Status: alpha** (`v0.2.0-alpha.1`). Config keys, the RBAC policy schema,
> and REST API paths may change between releases, and it is not yet recommended
> for unattended production use.

A lightweight, secure CLI tool for managing and accessing multiple Kubernetes clusters through a central control plane — without distributing kubeconfig files, opening inbound firewall rules, or requiring VPN access.

```bash
curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh | sh
```

## Problem

Giving an engineer access to a Kubernetes cluster usually means handing out a
kubeconfig or exposing the API server. That creates four problems that compound
with every new cluster, team, and environment.

- **Credential sprawl.** Every developer needs a kubeconfig for every cluster.
  Distributing, rotating, and revoking those credentials does not scale, and a
  credential on a laptop is a credential that can leak.
- **Network exposure.** Cluster API servers sit behind firewalls, so granting
  access means a VPN, a bastion, or a public endpoint. Each one is more attack
  surface to maintain.
- **Standing access, and no way to stop a mistake.** Permissions are granted
  once and kept forever, and Kubernetes RBAC decides only whether a *verb* on a
  *resource* is allowed. It cannot tell `delete pod api-0` from
  `delete pod --all`, so a single mistaken command reaches the cluster and the
  first anyone knows of it is the outage.
- **Records that do not answer the question.** Reconstructing who did what means
  stitching together logs from several clusters. And when the actor is an AI
  agent using a shared credential, the cluster's own audit log cannot say which
  human was behind it.

That last point is getting sharper. Teams are already letting AI agents inspect
and change infrastructure, often by handing them long-lived credentials, which
recreates every problem above at machine speed.

## Solution

kbridge puts a **control plane** between people and clusters. Nobody holds
cluster credentials: the user runs a CLI, each cluster runs a small agent that
dials *outbound*, and every command is checked before it is forwarded. No inbound
ports, no kubeconfig distribution, no VPN.

1. **Control plane (`kbridge-control-plane`)** authenticates the user, decides
   whether the command may run, routes it, and records the outcome. The one place
   access is granted and revoked.
2. **Cluster agent (`kbridge-agent`)** runs inside each cluster, opens an
   outbound gRPC connection, executes approved commands with kubectl, and returns
   the result. Because it dials out, no firewall change is needed, and because it
   holds the credentials, the control plane never does.
3. **CLI (`kb`)** speaks REST to the control plane with familiar kubectl syntax,
   so `kb get pods` works with no local credentials and no network path to the
   cluster.

Because the control plane sees the **command** and not just an API call, it can
do things a proxy cannot:

| Problem | What kbridge does |
|---|---|
| Credential sprawl | Users authenticate to the control plane; cluster credentials never leave the agent, and access is revoked centrally |
| Network exposure | The agent dials out, so no inbound port, bastion, or VPN is required |
| Standing access | Just-in-time grants: request a role for a bounded window with a reason, someone approves, and it expires on its own |
| Mistakes reaching the cluster | Guardrails evaluate the command before it is forwarded, so a destructive operation is refused rather than recorded |
| Records that do not answer the question | One audit log across every cluster, with the reason and the approval that admitted each command |

Work is organized into five tracks: credential-free access, just-in-time
authorization and guardrails, verifiable attribution and audit, governed access
for AI agents, and extending the same door to systems beyond Kubernetes. See
[ROADMAP.md](ROADMAP.md) for what is shipped, in progress, and planned.

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
|  - use         |  |  Control  |  +----------------------------------+
|  - kubectl     |<-|   Plane   |
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
                    | (SQLite)  |             |
                    |           |<------------+
                    +-----------+
```

### How It Works

```
CLI (kbridge) --HTTP REST--> Control Plane <--gRPC-- Agent (per cluster) --> kubectl
```

- **CLI to control plane**: REST for login, cluster listing, and one-shot
  commands; HTTP/2 bidirectional streams for `logs -f`, `exec -it`, and
  port-forward
- **Agent to control plane**: gRPC for registration, heartbeats, and command
  polling, plus one persistent stream that multiplexes every live session
- **Agent to Kubernetes**: kubectl, run locally with the agent's own
  ServiceAccount

Every command passes one authorization chokepoint: role rules decide first, then
guardrails may refuse it, demand a reason, or require an approved grant. Nothing
reaches a cluster without going through it.

See **[docs/architecture.md](docs/architecture.md)** for the request lifecycle,
the four command transports and their frame codecs, the authorization pipeline,
the data model, and the trust model.

## Components

### CLI (`kb`)

User-facing command-line tool. **kubectl by default** — anything that isn't a
management command (`login`, `logout`, `status`, `clusters`, `admin`) is run as
kubectl on the selected cluster. (`kbridge` remains as a back-compat alias.)

```bash
kb login                      # Login to the control plane
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

### Control Plane (`kbridge-control-plane`)

API gateway and control plane. Handles user authentication, cluster registry, RBAC enforcement, command proxying, and audit logging.

### Agent (`kbridge-agent`)

Lightweight daemon running in each Kubernetes cluster. Connects outbound to the control plane, registers cluster metadata, polls for commands, executes kubectl locally, and submits results back.

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
- `kbridge-control-plane` - control plane
- `kbridge-agent` - Cluster agent

### Run Locally

**1. Start the control plane:**
```bash
# Control Plane requires a real jwt_secret (>=32 chars). Generate one:
export KBRIDGE_JWT_SECRET="$(openssl rand -hex 32)"
./bin/kbridge-control-plane --config configs/control-plane.yaml
```

**2. Start an agent (in a cluster with kubectl access):**
```bash
./bin/kbridge-agent --config configs/agent.yaml
```

**3. Log in and use the CLI:**
```bash
# Default admin is seeded from control-plane.yaml (admin@kbridge.local / admin123 in
# the example config — change admin_password after first login for any real deployment).
./bin/kb login
./bin/kb clusters list
./bin/kb clusters use dev-cluster
./bin/kb get pods -A
```

See [docs/installation.md](docs/installation.md) for binary, Docker, and Helm
installs, and the [Documentation](#documentation) section below for full references.

## Configuration

### Control Plane (`control-plane.yaml`)

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
control_plane:
  url: localhost:9090              # control plane gRPC address
  token: dev-token                 # Authentication token

cluster:
  name: dev-cluster                # Unique cluster identifier
```

### CLI (`~/.kbridge/config.yaml`)

```yaml
control_plane_url: https://control-plane.example.com:8080
current_cluster: production-us-east
token: ""
```

### Environment Variables

**Agent:**

| Variable | Description | Default |
|----------|-------------|---------|
| `KBRIDGE_CONFIG` | Path to config file | `configs/agent.yaml` or `/etc/kbridge/agent.yaml` |
| `KBRIDGE_CONTROL_PLANE_URL` | control plane gRPC address | `localhost:9090` |
| `KBRIDGE_AGENT_TOKEN` | Authentication token | — |
| `KBRIDGE_CLUSTER_NAME` | Cluster name | `default` |

**control plane:**

| Variable | Description | Default |
|----------|-------------|---------|
| `KBRIDGE_CONFIG` | Path to config file | `configs/control-plane.yaml` |

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
        - name: KBRIDGE_CONTROL_PLANE_URL
          value: "control-plane.example.com:9090"
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

Users authenticate via `kb login`, which obtains a JWT token from the control plane and stores it locally. All subsequent API calls include this token.

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

### Guardrails

Roles decide what a person may touch. Guardrails decide whether a *particular
command* should run, and are evaluated after the roles so they can only take
access away. Because kbridge sees the command rather than just the API call, a
guardrail can tell `delete pod api-0` from `delete pod --all`, and `apply` from
`apply --dry-run=server` — distinctions cluster-side RBAC cannot express.

```yaml
guardrails:
  # Some commands should never run in production.
  - name: no-bulk-delete-in-prod
    match:
      clusters: ["prod-*"]
      verbs: ["delete"]
      args: ["--all"]
    action: deny
    message: "bulk delete is not allowed in production"

  # Others are fine, but must be justified. args_not exempts a dry run.
  - name: prod-writes-need-a-reason
    match:
      clusters: ["prod-*"]
      verbs: ["apply", "delete", "edit", "patch", "scale"]
      args_not: ["--dry-run*"]
    action: require-reason
```

A `require-reason` guardrail is satisfied with the `--reason` flag, and the
justification is stored on the audit entry:

```bash
kb delete pod api-0 --reason "INC-4521 rolling back bad deploy"
```

Test a policy before shipping it — both commands read the file directly, so they
run in CI, and `test` exits non-zero when the command would be refused:

```bash
kb policy validate -f configs/rbac.yaml
kb policy test -f configs/rbac.yaml -u alice@corp.com -c prod-eu -- delete ns payments
```

### Just-in-time access

Guardrails can also require that someone *else* says yes. A `require-approval`
guardrail is satisfied only by a time-boxed grant, so nobody changes production
alone and the access disappears on a timer instead of lingering.

```yaml
guardrails:
  - name: prod-workload-delete-needs-approval
    match:
      clusters: ["prod-*"]
      resources: ["deployments", "statefulsets"]
      verbs: ["delete"]
    action: require-approval
```

```bash
# Ask
kb request prod-eu --duration 2h --reason "INC-4521 rolling back bad deploy"
kb grants                                    # yours, with time remaining

# Decide (admin)
kb admin grants list --status pending
kb admin grants approve <id> --note "paged, go ahead"
kb admin grants revoke <id>                  # end one early
```

Point `grants.notify` at a Slack, Google Chat, or JSON webhook and every request
lands where approvers already are, with the approve command in the message.

A pending request grants nothing — the clock starts at approval, not at request
time — and self-approval is refused by default. Every step is audited, and a
command an approval admitted carries the same grant ID, so one query returns the
request, the decision, and everything run under it.

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

Server-authenticated TLS secures the HTTP API, the agent↔control plane gRPC channel,
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
| [Configuration](docs/configuration.md) | All control plane / agent / CLI options, incl. TLS |
| [CLI reference](docs/cli.md) | Every command with examples |
| [Architecture](docs/architecture.md) | Request lifecycle, command transports, authorization pipeline, data and trust models |
| [API reference](docs/api.md) | All HTTP endpoints |
| [RBAC](docs/rbac.md) | Policy file format and examples |
| [Admin guide](docs/admin.md) | Users, agent tokens, and audit logs |
| [Security hardening](docs/security.md) | Post-install checklist, two-layer authz, least-privilege agent RBAC |
| [Operations](docs/operations.md) | Backup/restore, upgrades, token rotation, troubleshooting |

## License

[Elastic License 2.0 (ELv2)](LICENSE) — free to use and modify. Commercial distribution and offering as a hosted/managed service are not permitted.
