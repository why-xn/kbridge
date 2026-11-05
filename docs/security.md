# Security Hardening Guide

This guide covers the security controls built into kbridge and the steps operators
must take to harden a deployment. It complements the
[installation guide](installation.md) and is distinct from
[SECURITY.md](../SECURITY.md) at the repo root, which is the vulnerability
disclosure policy.

---

## Post-install security checklist

Work through these items after every new deployment before exposing the control plane
service to users.

### 1. Change the admin password

The admin account is seeded from `auth.admin_email` / `auth.admin_password` in
`control-plane.yaml` (or the Helm chart's `auth.adminPassword`) on first startup.
Change it immediately via the API:

```bash
curl -s -X POST https://control-plane:8080/api/v1/auth/change-password \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"current_password":"<seed-password>","new_password":"<strong-password>"}'
```

`$TOKEN` is the access token returned by `POST /auth/login`. See the
[API reference](api.md#post-apiv1authchange-password) for the full request/response.

> The server logs a warning at startup when `admin_password` is `admin123` or
> shorter than 8 characters, but this is advisory — change it regardless.

### 2. Set a dedicated `token_pepper`

Agent tokens are stored as HMAC-SHA256 digests. By default the HMAC key falls
back to `jwt_secret`; set a separate key so a leak of one secret does not
compromise the other:

```yaml
# control-plane.yaml
auth:
  jwt_secret: "<generated>"
  token_pepper: "<separate-generated>"   # e.g. openssl rand -hex 32
```

Helm chart equivalent:

```yaml
auth:
  jwtSecret: "<generated>"
  tokenPepper: "<separate-generated>"
```

> Changing `token_pepper` invalidates all existing agent tokens. Re-issue them
> with `kb admin agent-tokens create` after rotating.

### 3. Enable TLS on the control plane and the agent

**control plane** (binary / Docker):

```yaml
# control-plane.yaml
tls:
  enabled: true
  cert_file: /etc/kbridge/tls.crt
  key_file:  /etc/kbridge/tls.key
```

**control plane** (Helm):

```yaml
tls:
  enabled: true
  secretName: kbridge-control-plane-tls   # kubernetes.io/tls secret
```

**Agent** — set the matching CA and enable TLS:

```yaml
# agent.yaml
control_plane:
  tls:
    enabled: true
    ca_file: /etc/kbridge/tls.crt   # PEM CA to verify control plane
    insecure: false                  # never set to true in production
```

**Agent** (Helm):

```yaml
control_plane:
  tls:
    enabled: true
    caCert: |
      -----BEGIN CERTIFICATE-----
      ...
      -----END CERTIFICATE-----
```

### 4. Disable `insecure_skip_verify` on the CLI

The CLI (`~/.kbridge/config.yaml`) defaults to `insecure_skip_verify: false`.
Confirm this is not accidentally set to `true` in any user config, especially on
machines that were used during development with self-signed certificates:

```yaml
# ~/.kbridge/config.yaml
insecure_skip_verify: false
```

### 5. Remove `bootstrap.agent_token`

The `bootstrap` block is a dev convenience that seeds one agent token at startup.
Remove it (or leave it blank) in production:

```yaml
# control-plane.yaml
bootstrap:          # omit entirely, or:
  agent_token: ""
  agent_cluster: ""
```

Helm chart:

```yaml
bootstrap:
  agentToken: ""
  agentCluster: ""
```

Use `kb admin agent-tokens create --cluster <name>` to provision tokens through
the admin API instead.

### 6. Use `existingSecret` — no plaintext secrets in Helm values

For Helm deployments, store `jwtSecret`, `tokenPepper`, and `adminPassword` in a
Kubernetes Secret and reference it rather than embedding values in `values.yaml`:

**control plane chart:**

```yaml
auth:
  existingSecret: kbridge-control-plane-secrets  # pre-created Secret name
  # jwtSecret, tokenPepper, adminPassword must NOT be set when existingSecret is used
```

The chart expects the Secret to contain the keys `jwt_secret`, `token_pepper`,
and `admin_password`. The control plane reads them from the mounted files via
`*_FILE` environment variables.

**Agent chart:**

```yaml
control_plane:
  existingSecret: kbridge-agent-token   # pre-created Secret with key `token`
  token: ""                             # leave empty when existingSecret is set
```

---

## Two-layer authorization

kbridge applies authorization at two independent layers. Both must be correctly
configured for meaningful defense in depth.

### Layer 1 — kbridge RBAC policy (what users may request)

The kbridge policy file (`rbac.policy_file` in `control-plane.yaml`) is enforced by
The control plane **before** any command reaches a cluster. It controls which authenticated
users may run which verbs on which resources in which clusters and namespaces:

```yaml
roles:
  - name: developer
    rules:
      - clusters: ["dev-*", "staging"]
        namespaces: ["*"]
        resources: ["pods", "deployments", "services"]
        verbs: ["get", "list", "logs", "exec"]

bindings:
  - subject: dev@corp.com
    roles: ["developer"]
```

A request denied by this layer is rejected at the control plane with `403` and recorded in
the audit log with status `denied` — the agent never sees it.

See [rbac.md](rbac.md) for the full policy reference including wildcards,
hot-reload, and the `default` role.

### Layer 2 — the agent (what kubectl can actually do)

The agent's ServiceAccount, bound to its ClusterRole, decides what the
Kubernetes API server will permit. kubectl itself never holds that credential.
For each command the agent starts kubectl with an empty kubeconfig and a
minimal environment, pointed by flags at a TLS proxy on loopback inside the
agent (the "window"), with a one-off nonce as its bearer token. The nonce is
valid until that command ends. The window:

- accepts only requests that carry a live nonce;
- strips any `Authorization` and `Impersonate-*` headers and forwards with the
  agent's own token, re-read from disk on every request so kubelet rotation
  takes effect;
- refuses any request under a protected namespace: the agent's own namespace
  (the Helm release namespace) and any listed in `protected_namespaces`
  (`protectedNamespaces` in the chart). This covers `exec`, `logs`,
  `port-forward`, `create token` and every object in those namespaces;
- refuses `GET /api/v1/secrets` and `/api/v1/watch/secrets`, the cluster-wide
  secrets list and watch.

The chart mounts the token as a projected volume at `/var/run/kbridge/sa`
(default 10 minute expiry) and leaves nothing at the default ServiceAccount
path.

Two argument checks run before kubectl starts, first on the control plane
(refusal is a `403` and a `denied` audit row) and again on the agent:

- **Connection flags** (`--server`, `--token`, `--kubeconfig`, `--context`,
  `--as`, client certificate and TLS flags and the like) are refused, so a
  command cannot point kubectl somewhere other than the window or swap
  credentials.
- **File flags** (`--from-file`, `--from-env-file`, `-k`, `--template`,
  `-f` with anything other than `-`, and similar) are refused, so kubectl
  never reads a file on the agent. The `kb` CLI reads local files and fetches
  `-f <URL>` itself and sends the content on stdin. The verbs `cp`, `proxy`,
  `plugin`, `config` and `kustomize` are refused outright.

**Limit: kubectl runs as the agent's uid.** It can read the token file the
same way the agent does. The window keeps the token out of kubectl's
arguments, kubeconfig and environment, but the flag checks above are what
stop a command from reading the token file or sending it elsewhere. A gap in
those checks is a token leak. The structural fix is to run kubectl under a second uid that
cannot read the token; it is not done yet.

The window narrows what a user can reach through kbridge. It does not narrow
what a compromised agent can do: that is still the ClusterRole.
**A locked-down kbridge policy combined with a `cluster-admin` agent
ServiceAccount is NOT defense in depth.** The agent chart default `rbac.rules`
grants `*/*` on all verbs, equivalent to `cluster-admin`. Scope it down to the
minimum your users actually need (see the next section), and consider the
chart's optional `networkPolicy`, which limits agent egress to the API server,
the control plane and cluster DNS.

### How the layers interact

| kbridge policy | Agent ClusterRole | Outcome |
|----------------|-------------------|---------|
| allows `get pods` | grants `get pods` | Request succeeds |
| denies `delete pods` | grants `delete pods` | Rejected at the control plane; agent never called |
| allows `delete pods` | denies `delete pods` | control plane forwards; Kubernetes rejects it |
| **allows `delete nodes`** | **grants `*`** | **Full cluster access if agent is compromised** |

The agent ClusterRole is the floor: it determines the blast radius of an agent
compromise. Keep it as narrow as the use case permits.

---

## Agent least-privilege ClusterRole

The agent chart creates a ClusterRole from `rbac.rules` in `values.yaml`. The
chart default is unrestricted:

```yaml
# charts/agent/values.yaml (default — do not use in production)
rbac:
  rules:
    - apiGroups: ["*"]
      resources: ["*"]
      verbs: ["*"]
```

Override `rbac.rules` in your installation to match what your users actually do.

### Viewer-equivalent (read-only)

Suitable for teams that need visibility but should not modify workloads:

```yaml
# values-agent.yaml
rbac:
  rules:
    - apiGroups: ["", "apps", "batch"]
      resources:
        - pods
        - pods/log
        - services
        - deployments
        - replicasets
        - jobs
        - configmaps
        - nodes
        - events
      verbs: ["get", "list", "watch"]
```

### Developer-equivalent (read/write workloads)

Suitable for development clusters where users need exec, apply, and restart:

```yaml
# values-agent.yaml
rbac:
  rules:
    - apiGroups: ["", "apps"]
      resources:
        - pods
        - pods/exec
        - pods/portforward
        - deployments
        - services
        - configmaps
      verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

Apply either ruleset at install or upgrade time:

```bash
helm install kbridge-agent ./charts/agent \
  --set control_plane.url=kbridge-control-plane:9090 \
  --set control_plane.token=<token> \
  --set cluster.name=prod-us-east \
  -f values-agent.yaml
```

> The kbridge RBAC policy on the control plane is still the per-user gate. A developer
> whose kbridge policy only allows `get`/`list` cannot run `delete` even if the
> ClusterRole permits it. The ClusterRole sets the ceiling; the policy sets the
> per-user floor.

---

## What the platform enforces

These controls are built into kbridge and are not configurable away. They
provide a hardened baseline regardless of how the deployment is configured.

### Strong-secret enforcement (fail-closed)

The control plane refuses to start if `jwt_secret` is not set, is shorter than 32
characters, or matches the shipped development default
(`"change-me"` variants). The startup check is fail-closed: a misconfigured
secret is a fatal error, not a warning. Generate a secret with:

```bash
openssl rand -hex 32
```

### Short-lived access tokens with transparent refresh

Access tokens expire after **1 hour** (both the compiled default and the Helm
chart default — tighten with `auth.accessTokenExpiry` for higher-security
environments). The CLI refreshes tokens transparently in the background; users
are not prompted to log in until the refresh token expires. Running `kb logout`
invalidates the refresh token on the server immediately — subsequent refresh
attempts are rejected.

### Login rate limiting

The `/auth/login` endpoint is rate-limited. Clients that exceed the limit receive
`429 Too Many Requests`. This applies per-IP and protects against credential
stuffing and brute-force attacks.

### Agent tokens hashed with HMAC + pepper

Agent tokens are stored only as an HMAC-SHA256 digest keyed by `token_pepper`
(falling back to `jwt_secret` when `token_pepper` is not set). The plaintext
token is shown exactly once at creation and never stored. A stolen database
cannot be used to verify or forge tokens without the pepper. See the
[admin guide](admin.md#agent-tokens) for rotation procedures.

### Audit log of every command

Every command attempt — allowed, denied, failed, or timed out — is recorded in
the audit log with: user, cluster, namespace, command, exit code, duration, and
client IP. The log is queryable via `kb admin audit` or
`GET /api/v1/admin/audit`. Denied requests are included (status `denied`), so
the audit log is also useful for detecting over-broad expectations in the RBAC
policy.

Logs are retained for `audit.retention_days` (default 90 days) and pruned
automatically.
