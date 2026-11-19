# Configuration Reference

All components follow the same pattern: `DefaultConfig() → LoadConfig(path) →
Validate()`. Configs are YAML; the control plane and the agent also honour the
environment variables listed under each component. The CLI reads only its
config file and the editor variables (`KUBE_EDITOR`, `EDITOR`, `VISUAL`).

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

ui:
  enabled: true            # serve the admin console at /
```

| Key | Required | Notes |
|-----|----------|-------|
| `auth.jwt_secret` | yes | Signing key for access tokens; at least 32 characters, and published placeholders are refused |
| `auth.token_pepper` | no | HMAC key for agent tokens at rest; when empty, falls back to `jwt_secret`. Set a dedicated value for key separation. Changing it invalidates existing agent tokens (re-issue them) |
| `database.path` | yes | SQLite file path |
| `bootstrap.*` | no | Seeds one agent token at startup; prefer the admin API |
| `rbac.policy_file` | no | When empty, all authenticated users are allowed |
| `tls.*` | no | When `enabled`, `cert_file` + `key_file` are required |
| `streams.max_concurrent` | no | Cap on concurrent streaming sessions; `0`/unset → default 50 |
| `ui.enabled` | no | Serve the admin console at `/` (default `true`); `KBRIDGE_UI_ENABLED=false` turns it off |
| `auth.jwt_secret_file` / `auth.token_pepper_file` / `auth.admin_password_file` | no | Read the secret from a file instead of the key above; the Helm chart uses these for `auth.existingSecret` |

### Control plane environment variables

| Variable | Overrides | Default |
|----------|-----------|---------|
| `KBRIDGE_CONFIG` | config file path (`--config` wins) | none: with neither, the control plane runs on built-in defaults, with RBAC off and no seeded admin, so `KBRIDGE_JWT_SECRET` is required |
| `KBRIDGE_JWT_SECRET` / `KBRIDGE_JWT_SECRET_FILE` | `auth.jwt_secret` / `auth.jwt_secret_file` | — |
| `KBRIDGE_TOKEN_PEPPER` / `KBRIDGE_TOKEN_PEPPER_FILE` | `auth.token_pepper` / `auth.token_pepper_file` | — |
| `KBRIDGE_ADMIN_PASSWORD` / `KBRIDGE_ADMIN_PASSWORD_FILE` | `auth.admin_password` / `auth.admin_password_file` | — |
| `KBRIDGE_UI_ENABLED` | `ui.enabled` | `true` |

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

health_file: /tmp/kbridge-agent-healthy   # set in the file only; no env var
```

| Key | Env var | Default | Meaning |
|-----|---------|---------|---------|
| `control_plane.token_file` | `KBRIDGE_AGENT_TOKEN_FILE` | empty | File holding the agent token; the Helm chart sets it for `controlPlane.existingSecret`. Precedence: `KBRIDGE_AGENT_TOKEN_FILE`, `KBRIDGE_AGENT_TOKEN`, `AGENT_TOKEN`, `token_file`, `token` |
| `health_file` | — | `/tmp/kbridge-agent-healthy` | Touched while the agent is connected; the chart's exec probe reads it |
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
| `KBRIDGE_AGENT_TOKEN_FILE` | `control_plane.token_file` | — |
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
4. For the CLI, use an `https://` `control_plane_url`. With a certificate from a
   private CA, add the CA to the system trust store or, on Linux, run the CLI
   with `SSL_CERT_FILE=/path/to/ca.crt`. Set `insecure_skip_verify: true` only
   against a development control plane (see
   [security.md](security.md#4-trust-the-control-planes-ca-on-the-cli-keep-insecure_skip_verify-off)).

The same certificate secures both the HTTP and gRPC servers.
