# Configuration Reference

All components follow the same pattern: `DefaultConfig() → LoadConfig(path) →
Validate()`. Configs are YAML; the agent and CLI also honour environment
variables.

## Control Plane (`control-plane.yaml`)

```yaml
server:
  http_port: 8080          # REST API port
  grpc_port: 9090          # gRPC (agent) port

database:
  driver: sqlite           # SQLite only (PostgreSQL is not yet supported)
  path: kbridge.db

auth:
  jwt_secret: "..."        # REQUIRED; use a long random value in production
  token_pepper: ""         # HMAC key for agent tokens at rest; empty = reuse jwt_secret
  access_token_expiry: 1h
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
| `auth.token_pepper` | no | HMAC key for agent tokens at rest; when empty, falls back to `jwt_secret`. Set a dedicated value for key separation. Changing it invalidates existing agent tokens (re-issue them) |
| `database.path` | yes | SQLite file path |
| `bootstrap.*` | no | Seeds one agent token at startup; prefer the admin API |
| `rbac.policy_file` | no | When empty, all authenticated users are allowed |
| `tls.*` | no | When `enabled`, `cert_file` + `key_file` are required |
| `streams.max_concurrent` | no | Cap on concurrent streaming sessions; `0`/unset → default 50 |
| `grants.max_duration` / `grants.default_duration` | no | Bounds on just-in-time grants; unset → 8h / 1h |
| `grants.notify[]` | no | Webhooks for grant events. `url` and `secret` each accept a `_file` variant and env overrides `KBRIDGE_GRANTS_NOTIFY_<n>_URL` / `_SECRET` (+ `_FILE`), indexed by position. A chat webhook URL is a credential; keep it out of the file. See [rbac.md](rbac.md#notifications) |

## Agent (`agent.yaml`)

```yaml
control_plane:
  url: localhost:9090      # control plane gRPC address
  token: dev-token         # agent token (bound to one cluster)
  tls:
    enabled: false
    ca_file: "certs/tls.crt"   # CA to verify control plane; empty = system roots
    insecure: false            # skip verification (dev only)

cluster:
  name: dev-cluster        # unique cluster identifier (must match the token)

kubernetes:                # how the agent reaches its own API server (in-cluster defaults shown)
  server: ""               # https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT when set in the pod
  ca_file: /var/run/kbridge/sa/ca.crt
  token_file: /var/run/kbridge/sa/token
  kubeconfig: ""           # dev/e2e only; overrides the three keys above

protected_namespaces: []   # namespaces kubectl through kbridge may not touch

sync:
  interval: 60s            # how often role bindings are re-checked against policy
```

| Key | Env var | Default | Meaning |
|-----|---------|---------|---------|
| `kubernetes.server` | `KBRIDGE_KUBERNETES_SERVER` | `https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT` (port `443` if unset; empty outside a pod) | API server the agent's kubectl is pointed at |
| `kubernetes.ca_file` | `KBRIDGE_KUBERNETES_CA_FILE` | `/var/run/kbridge/sa/ca.crt` | CA that verifies the API server |
| `kubernetes.token_file` | `KBRIDGE_KUBERNETES_TOKEN_FILE` | `/var/run/kbridge/sa/token` | ServiceAccount token the agent uses for impersonation and binding sync |
| `kubernetes.kubeconfig` | `KBRIDGE_KUBECONFIG` | empty | Kubeconfig to use instead of the three keys above. Development and e2e only: reads the current context, with a token or client certificate; `exec` and `auth-provider` users are not supported. `~` is not expanded, so use an absolute path |
| `protected_namespaces` | `KBRIDGE_PROTECTED_NAMESPACES` (comma-separated) | empty | Namespaces the agent refuses to run commands in, in addition to its own |
| `sync.interval` | `KBRIDGE_SYNC_INTERVAL` | `60s` | Full re-check period for RoleBinding sync (Go duration); must be positive, an unparsable env value fails validation |

Validation: `kubernetes.server` and `kubernetes.token_file` are required unless
`kubernetes.kubeconfig` is set.

### Agent environment variables

| Variable | Overrides | Default |
|----------|-----------|---------|
| `KBRIDGE_CONFIG` | config file path | `configs/agent.yaml` |
| `KBRIDGE_CONTROL_PLANE_URL` | `control_plane.url` | `localhost:9090` |
| `KBRIDGE_AGENT_TOKEN` / `AGENT_TOKEN` | `control_plane.token` | — |
| `KBRIDGE_CLUSTER_NAME` | `cluster.name` | `default` |
| `KBRIDGE_KUBERNETES_SERVER`, `KBRIDGE_KUBERNETES_CA_FILE`, `KBRIDGE_KUBERNETES_TOKEN_FILE`, `KBRIDGE_KUBECONFIG`, `KBRIDGE_PROTECTED_NAMESPACES`, `KBRIDGE_SYNC_INTERVAL` | see the table above | see the table above |

## CLI (`~/.kbridge/config.yaml`)

```yaml
control_plane_url: https://control-plane.example.com:8080
current_cluster: production-us-east
token: ""                  # set by `kb login`
refresh_token: ""
insecure_skip_verify: false   # skip TLS verification for self-signed dev certs
```

## TLS

Server-authenticated TLS protects the HTTP API, the agent↔control plane gRPC channel,
and the CLI.

1. Generate a dev certificate (localhost + 127.0.0.1):

   ```bash
   make certs           # writes certs/tls.crt and certs/tls.key
   ```

2. Enable on the control plane (`tls.enabled: true`, point at the cert/key).
3. On the agent, set `control_plane.tls.enabled: true` and either `ca_file: certs/tls.crt`
   (verify) or `insecure: true` (skip — dev only).
4. For the CLI, use an `https://` `control_plane_url`; with a self-signed cert set
   `insecure_skip_verify: true`.

The same certificate secures both the HTTP and gRPC servers.
