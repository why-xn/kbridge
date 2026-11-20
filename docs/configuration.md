# Configuration Reference

This page is the one list of every setting. Other pages link here rather than
repeating it.

## Three ways to set a value

| Way | Where it applies | How |
|-----|------------------|-----|
| Config file | control plane, agent, CLI | YAML; the control plane and agent read the file named by `--config` (or `KBRIDGE_CONFIG`), the CLI reads `~/.kbridge/config.yaml` |
| Environment variable | control plane, agent | the variables in each table below |
| Helm value | control plane chart, agent chart | the chart renders the config file from its values; with `auth.existingSecret` or `controlPlane.existingSecret` it also sets the `*_FILE` variables |

Precedence, highest first: environment variable, then config file, then the
built-in default. For a secret (the JWT secret, the token pepper, the admin
password, the agent token, each grant webhook URL and secret) the order is
the `_FILE` environment variable, the plain environment variable, the
`*_file` config key, then the inline value. A Helm install sets everything
through values, which end up in the config file; the chart sets no other
environment variables.

All components load configuration the same way: `DefaultConfig() →
LoadConfig(path) → Validate()`. The CLI reads only its config file and the
editor variables (`KUBE_EDITOR`, `EDITOR`, `VISUAL`).

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

| Key | Env var | Helm value | Default | What it does |
|-----|---------|------------|---------|--------------|
| config file path | `KBRIDGE_CONFIG` (`--config` wins) | set by the chart | none: with neither, the control plane runs on built-in defaults, with RBAC off and no seeded admin, so `KBRIDGE_JWT_SECRET` is required | Which YAML file to read |
| `server.http_port` | — | `service.httpPort` | `8080` | REST API, console and `/health` |
| `server.grpc_port` | — | `service.grpcPort` | `9090` | gRPC port agents dial |
| `database.driver` | — | — | `sqlite` | SQLite only (PostgreSQL is not yet supported) |
| `database.path` | — | `persistence.dbPath` | `kbridge.db` (chart: `/data/kbridge.db`) | SQLite file path |
| `auth.jwt_secret` | `KBRIDGE_JWT_SECRET` | `auth.jwtSecret` | none, required | Signing key for access tokens; at least 32 characters, and published placeholders are refused |
| `auth.jwt_secret_file` | `KBRIDGE_JWT_SECRET_FILE` | `auth.existingSecret` (key `jwt_secret`) | — | Read the JWT secret from a file |
| `auth.token_pepper` | `KBRIDGE_TOKEN_PEPPER` | `auth.tokenPepper` | empty: falls back to `jwt_secret` | HMAC key for agent tokens at rest. Set a dedicated value for key separation. Changing it invalidates existing agent tokens (re-issue them) |
| `auth.token_pepper_file` | `KBRIDGE_TOKEN_PEPPER_FILE` | `auth.existingSecret` (key `token_pepper`) | — | Read the pepper from a file |
| `auth.access_token_expiry` | — | `auth.accessTokenExpiry` | `1h` | Access token lifetime |
| `auth.refresh_token_expiry` | — | `auth.refreshTokenExpiry` | `168h` | Refresh token lifetime; also ends a console session |
| `auth.admin_email` | — | `auth.adminEmail` | empty (chart: `admin@kbridge.local`) | The admin account seeded on first start, when both email and password are set |
| `auth.admin_password` | `KBRIDGE_ADMIN_PASSWORD` | `auth.adminPassword` | empty (chart: `change-me`) | That account's first password. Only seeds: once the account exists, changing it does nothing |
| `auth.admin_password_file` | `KBRIDGE_ADMIN_PASSWORD_FILE` | `auth.existingSecret` (key `admin_password`) | — | Read the admin password from a file |
| `auth.admin_name` | — | `auth.adminName` | `Admin` | Display name of the seeded admin |
| `audit.retention_days` | — | `audit.retentionDays` | `90` | Audit rows older than this are pruned |
| `audit.cleanup_interval` | — | `audit.cleanupInterval` | `24h` | How often the prune job runs |
| `bootstrap.agent_token` / `bootstrap.agent_cluster` | — | `bootstrap.agentToken` / `bootstrap.agentCluster` | empty | Seeds one agent token at startup, for development; prefer `kb admin agent-tokens create` |
| `rbac.policy_file` | — | `rbac.policy` (the policy itself; the chart writes it to a file) | empty: all authenticated users are allowed (chart: a policy with `admin` and `viewer`) | The [RBAC policy](rbac.md) |
| `tls.enabled` / `tls.cert_file` / `tls.key_file` | — | `tls.enabled` / `tls.secretName` (a `kubernetes.io/tls` Secret) | `false` | When `enabled`, `cert_file` + `key_file` are required; see [TLS](#tls) |
| `streams.max_concurrent` | — | `streams.maxConcurrent` | `50` (`0`/unset → 50) | Cap on concurrent streaming sessions |
| `ui.enabled` | `KBRIDGE_UI_ENABLED` | `ui.enabled` | `true` | Serve the [admin console](console.md) at `/` |
| `grants.max_duration` / `grants.default_duration` | — | — | `8h` / `1h` | **Next release.** Bounds on just-in-time grants |
| `grants.allow_self_approval` | — | — | `false` | **Next release.** Let a requester approve their own grant |
| `grants.notify[]` | `KBRIDGE_GRANTS_NOTIFY_<n>_URL` / `_SECRET` (+ `_FILE`) | — | — | **Next release.** Webhooks for grant events. `url` and `secret` each accept a `_file` variant; the env overrides are indexed by position. A chat webhook URL is a credential; keep it out of the file. See [rbac.md](rbac.md#notifications) |

Chart values that are not config keys (image, persistence, ingress,
resources) are described in the chart's
[values.yaml](../charts/control-plane/values.yaml).

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

| Key | Env var | Helm value | Default | What it does |
|-----|---------|------------|---------|--------------|
| config file path | `KBRIDGE_CONFIG` (`--config` wins) | set by the chart | `configs/agent.yaml`, then `/etc/kbridge/agent.yaml`, else built-in defaults | Which YAML file to read |
| `control_plane.url` | `KBRIDGE_CONTROL_PLANE_URL` | `controlPlane.url` | `localhost:9090` (chart: `kbridge-control-plane:9090`) | Control plane gRPC address |
| `control_plane.token` | `KBRIDGE_AGENT_TOKEN` / `AGENT_TOKEN` | `controlPlane.token` | none, required | Agent token, bound to one cluster |
| `control_plane.token_file` | `KBRIDGE_AGENT_TOKEN_FILE` | `controlPlane.existingSecret` (key `token`) | empty | File holding the agent token. Precedence: `KBRIDGE_AGENT_TOKEN_FILE`, `KBRIDGE_AGENT_TOKEN`, `token_file`, `token`; `AGENT_TOKEN` is read only when none of those is set |
| `control_plane.tls.enabled` | — | `controlPlane.tls.enabled` | `false` | TLS on the gRPC connection |
| `control_plane.tls.ca_file` | — | `controlPlane.tls.caCert` (the PEM itself; mounted as `/etc/kbridge/ca.crt`) | empty: system roots | CA that verifies the control plane |
| `control_plane.tls.insecure` | — | `controlPlane.tls.insecure` | `false` | Skip certificate verification; development only, never in production |
| `cluster.name` | `KBRIDGE_CLUSTER_NAME` | `cluster.name` | `default` (chart: `my-cluster`) | Cluster identifier; must match the token's `--cluster` |
| `kubernetes.server` | `KBRIDGE_KUBERNETES_SERVER` | — | `https://$KUBERNETES_SERVICE_HOST:$KUBERNETES_SERVICE_PORT` (port `443` if unset; empty outside a pod) | API server the agent's kubectl is pointed at |
| `kubernetes.ca_file` | `KBRIDGE_KUBERNETES_CA_FILE` | — | `/var/run/kbridge/sa/ca.crt` | CA that verifies the API server |
| `kubernetes.token_file` | `KBRIDGE_KUBERNETES_TOKEN_FILE` | — | `/var/run/kbridge/sa/token` | ServiceAccount token the agent uses for impersonation and binding sync |
| `kubernetes.kubeconfig` | `KBRIDGE_KUBECONFIG` | — | empty | Kubeconfig to use instead of the three keys above. Development and e2e only: reads the current context, with a token or client certificate; `exec` and `auth-provider` users are not supported. `~` is not expanded, so use an absolute path |
| `protected_namespaces` | `KBRIDGE_PROTECTED_NAMESPACES` (comma-separated) | `protectedNamespaces` | empty | Namespaces the agent refuses every request for by path (exec, logs, port-forward, reads, writes), in addition to its own. A cluster-wide list such as `kb get pods -A` still shows their objects, except Secrets. The chart always adds the release namespace; add the control plane's namespace when it runs in the same cluster. Adding one does not remove bindings already made there; see [Known limits](security.md#known-limits) |
| `sync.interval` | `KBRIDGE_SYNC_INTERVAL` | `sync.interval` | `60s` | Full re-check period for RoleBinding sync when nothing else triggers one, e.g. after a namespace is labelled for Pod Security (Go duration); must be positive, an unparsable env value fails validation |
| `health_file` | — | — | `/tmp/kbridge-agent-healthy` | Touched while the agent is connected; the chart's exec probe reads it. Set in the file only |

Validation: `kubernetes.server` and `kubernetes.token_file` are required unless
`kubernetes.kubeconfig` is set.

### Agent chart values

These chart values have no config key: they shape the agent's Kubernetes
permissions and network access.

The agent never hands its ServiceAccount token to kubectl. The chart turns off
the default token mount and projects the token, CA and namespace read-only
at `/var/run/kbridge/sa` (`defaultMode: 0400`; the kubelet adds group read for
the pod's `fsGroup: 10001`, the agent user).

| Value | Default | Purpose |
|---|---|---|
| `accessCeiling.clusterRoles` | `[view, edit, cluster-admin]` | ClusterRoles the agent may bind for kbridge users. Must not be empty. Lowering it does not remove bindings already made; see [Known limits](security.md#known-limits). |
| `accessCeiling.namespaces` | `[]` | Namespaces the agent may write RoleBindings in; empty means any. Must not include the release namespace. |
| `accessCeiling.clusterWide` | `true` | Allow ClusterRoleBindings (`clusterWide: true` in policy). |
| `serviceAccountToken.expirationSeconds` | `600` | Lifetime of the projected token; the kubelet rotates it. |
| `networkPolicy.enabled` | `false` | Render an egress NetworkPolicy (recommended). Requires `apiServerCIDRs` and `controlPlaneTo`; rendering fails without them. |
| `networkPolicy.apiServerCIDRs` | `[]` | API server endpoint IPs, e.g. `kubectl -n default get endpointslice kubernetes -o jsonpath='{.endpoints[*].addresses[*]}'` as `/32`s. |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | API server ports. |
| `networkPolicy.controlPlaneTo` | `[]` | NetworkPolicyPeer list for the control plane, e.g. `[{ipBlock: {cidr: 203.0.113.10/32}}]`. |
| `networkPolicy.controlPlanePort` | `9090` | Control plane gRPC port. |

With the policy on, the agent can reach only the API server, the control plane
and `kube-dns` in `kube-system` (53/UDP and TCP).

## CLI (`~/.kbridge/config.yaml`)

```yaml
control_plane_url: https://control-plane.example.com
current_cluster: production-us-east
token: ""                  # set by `kb login`
refresh_token: ""
insecure_skip_verify: false   # skip TLS verification for self-signed dev certs
```

| Key | Default | What it does |
|-----|---------|--------------|
| `control_plane_url` | empty | Control plane URL; the first `kb login` asks for it and saves it |
| `current_cluster` | empty | Cluster commands run on; set by `kb clusters use` |
| `token` / `refresh_token` | empty | Set by `kb login`, cleared by `kb logout`. The file is written `0600` |
| `insecure_skip_verify` | `false` | Skip TLS verification, against a development control plane only |

The CLI has no Helm value and reads no `KBRIDGE_*` variable. It does read:

| Env var | What it does |
|---------|--------------|
| `KUBE_EDITOR`, `EDITOR`, `VISUAL` | Editor for `kb edit`, first one set wins (default `vi`); see [cli.md](cli.md#running-commands) |
| `SSL_CERT_FILE` | On Linux, a CA file to trust for a control plane with a private CA; see [security.md](security.md#4-trust-the-control-planes-ca-on-the-cli-keep-insecure_skip_verify-off) |

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
   private CA, give the CLI the CA as
   [security.md](security.md#4-trust-the-control-planes-ca-on-the-cli-keep-insecure_skip_verify-off)
   describes.

The same certificate secures both the HTTP and gRPC servers.
