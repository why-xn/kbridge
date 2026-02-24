# kbridge

A lightweight, secure CLI tool for managing and accessing multiple Kubernetes clusters through a central gateway — without distributing kubeconfig files, opening inbound firewall rules, or requiring VPN access.

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
3. **CLI (`kbridge`)** — A user-friendly command-line tool that talks to central over REST. Developers use familiar kubectl syntax (`kbridge kubectl get pods`) without needing direct cluster credentials or network access.

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

### CLI (`kbridge`)

User-facing command-line tool.

```bash
kbridge login                      # Login to central service
kbridge logout                     # Logout
kbridge clusters list              # List available clusters
kbridge clusters use <cluster>     # Select active cluster
kbridge kubectl get pods           # Run kubectl on selected cluster
kbridge kubectl apply -f app.yaml  # Any kubectl command works
kbridge k get pods                 # 'k' alias for kubectl
kbridge status                     # Show current context
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
- `kbridge` - CLI tool
- `kbridge-central` - Central service
- `kbridge-agent` - Cluster agent

### Run Locally

**1. Start the central service:**
```bash
./bin/kbridge-central --config configs/central.yaml
```

**2. Start an agent (in a cluster with kubectl access):**
```bash
./bin/kbridge-agent --config configs/agent.yaml
```

**3. Use the CLI:**
```bash
./bin/kbridge clusters list
./bin/kbridge clusters use dev-cluster
./bin/kbridge kubectl get pods -A
```

## Configuration

### Central Service (`central.yaml`)

```yaml
server:
  http_port: 8080    # REST API port
  grpc_port: 9090    # gRPC server port
```

### Agent (`agent.yaml`)

```yaml
central:
  url: localhost:9090              # Central gRPC address
  token: dev-token                 # Authentication token

cluster:
  name: dev-cluster                # Unique cluster identifier
  kubernetes_version: "1.28.0"
  node_count: 3
  region: us-east-1
  provider: aws
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
| `KBRIDGE_CLUSTER_REGION` | Cloud region | `unknown` |
| `KBRIDGE_CLUSTER_PROVIDER` | Cloud provider | `unknown` |

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

Users authenticate via `kbridge login`, which obtains a JWT token from central and stores it locally. All subsequent API calls include this token.

### RBAC (Planned)

Roles define access by cluster, namespace, resource, and verb with wildcard support:

```yaml
roles:
  - name: developer
    clusters:
      - name: "dev-*"
        namespaces: ["*"]
        verbs: ["get", "list", "logs", "exec"]
        resources: ["pods", "services", "deployments"]

  - name: admin
    clusters:
      - name: "*"
        namespaces: ["*"]
        verbs: ["*"]
        resources: ["*"]
```

### Agent Authentication

Agents authenticate with pre-shared tokens during registration. TLS for all communication is planned.

## Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go |
| CLI | Cobra + Viper |
| HTTP Server | Gin |
| RPC | gRPC + Protocol Buffers |
| Database | SQLite (dev) / PostgreSQL (prod) - planned |
| Auth | JWT (RS256) - planned |
| Config | YAML + environment variables |

## License

MIT
