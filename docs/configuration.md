# Configuration Reference

All components follow the same pattern: `DefaultConfig() → LoadConfig(path) →
Validate()`. Configs are YAML; the agent and CLI also honour environment
variables.

## Central (`central.yaml`)

```yaml
server:
  http_port: 8080          # REST API port
  grpc_port: 9090          # gRPC (agent) port

database:
  driver: sqlite           # sqlite (postgres planned)
  path: kbridge.db

auth:
  jwt_secret: "..."        # REQUIRED; use a long random value in production
  access_token_expiry: 24h
  refresh_token_expiry: 168h
  admin_email: admin@kbridge.local    # seeded on first start if set
  admin_password: changeme
  admin_name: Admin

audit:
  retention_days: 90       # logs older than this are pruned
  cleanup_interval: 24h    # how often the prune job runs

bootstrap:                 # optional dev convenience; omit in production
  agent_token: "dev-token"
  agent_cluster: "dev-cluster"

rbac:
  policy_file: "configs/rbac.yaml"   # empty disables enforcement (allow-all)

tls:
  enabled: false
  cert_file: "certs/tls.crt"
  key_file: "certs/tls.key"

streams:
  max_concurrent: 50       # max simultaneous streaming sessions (logs -f / get -w)
```

| Key | Required | Notes |
|-----|----------|-------|
| `auth.jwt_secret` | yes | Signing key for access tokens |
| `database.path` | yes | SQLite file path |
| `bootstrap.*` | no | Seeds one agent token at startup; prefer the admin API |
| `rbac.policy_file` | no | When empty, all authenticated users are allowed |
| `tls.*` | no | When `enabled`, `cert_file` + `key_file` are required |
| `streams.max_concurrent` | no | Cap on concurrent streaming sessions; `0`/unset → default 50 |

## Agent (`agent.yaml`)

```yaml
central:
  url: localhost:9090      # central gRPC address
  token: dev-token         # agent token (bound to one cluster)
  tls:
    enabled: false
    ca_file: "certs/tls.crt"   # CA to verify central; empty = system roots
    insecure: false            # skip verification (dev only)

cluster:
  name: dev-cluster        # unique cluster identifier (must match the token)
  kubernetes_version: "1.28.0"
  node_count: 3
  region: us-east-1
  provider: aws
```

### Agent environment variables

| Variable | Overrides | Default |
|----------|-----------|---------|
| `KBRIDGE_CONFIG` | config file path | `configs/agent.yaml` |
| `KBRIDGE_CENTRAL_URL` | `central.url` | `localhost:9090` |
| `KBRIDGE_AGENT_TOKEN` / `AGENT_TOKEN` | `central.token` | — |
| `KBRIDGE_CLUSTER_NAME` | `cluster.name` | `default` |
| `KBRIDGE_CLUSTER_REGION` | `cluster.region` | `unknown` |
| `KBRIDGE_CLUSTER_PROVIDER` | `cluster.provider` | `unknown` |

## CLI (`~/.kbridge/config.yaml`)

```yaml
central_url: https://central.example.com:8080
current_cluster: production-us-east
token: ""                  # set by `kbridge login`
refresh_token: ""
insecure_skip_verify: false   # skip TLS verification for self-signed dev certs
```

## TLS

Server-authenticated TLS protects the HTTP API, the agent↔central gRPC channel,
and the CLI.

1. Generate a dev certificate (localhost + 127.0.0.1):

   ```bash
   make certs           # writes certs/tls.crt and certs/tls.key
   ```

2. Enable on central (`tls.enabled: true`, point at the cert/key).
3. On the agent, set `central.tls.enabled: true` and either `ca_file: certs/tls.crt`
   (verify) or `insecure: true` (skip — dev only).
4. For the CLI, use an `https://` `central_url`; with a self-signed cert set
   `insecure_skip_verify: true`.

The same certificate secures both the HTTP and gRPC servers.
