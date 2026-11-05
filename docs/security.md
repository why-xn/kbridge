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

kbridge applies authorization at two independent layers, in order: the kbridge
policy on the control plane, then Kubernetes RBAC on the cluster, evaluated as
the requesting user. A command runs only when both allow it.

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

Every command runs as the user who sent it. The control plane sends the
user's identity with each command, stream and port-forward, and the agent
impersonates `kbridge:<email>` on every request to the API server, so
Kubernetes RBAC bound to that user decides what the API server permits. The
agent refuses a command that carries no identity, and the control plane
answers `503` for an agent too old to impersonate. The agent's own
ClusterRole grants impersonation and what it needs to keep those users'
bindings in step with policy, up to the chart's `accessCeiling` (see
[Binding kbridge users in the cluster](#binding-kbridge-users-in-the-cluster)).
kubectl itself never holds the agent's credential.
For each command the agent starts kubectl with an empty kubeconfig and a
minimal environment, pointed by flags at a TLS proxy on loopback inside the
agent (the "window"), with a one-off nonce as its bearer token. The nonce is
valid until that command ends. The window:

- accepts only requests that carry a live nonce;
- strips any `Authorization` and `Impersonate-*` headers and forwards with the
  agent's own token, re-read from disk on every request so kubelet rotation
  takes effect, adding `Impersonate-User: kbridge:<email>` and
  `Impersonate-Extra-Kbridge-Request-Id: <request id>`;
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
what a compromised agent can do. Kubernetes cannot limit impersonation to a
name prefix, so the agent may impersonate any user name (not groups or service
accounts): a compromised agent holds every binding whose subject is a `User`,
`kbridge:` or not. Prefer binding people outside kbridge by group, and consider the
chart's optional `networkPolicy`, which limits agent egress to the API server,
the control plane and cluster DNS.

### How the layers interact

| kbridge policy | Kubernetes RBAC for `kbridge:<email>` | Outcome |
|----------------|---------------------------------------|---------|
| allows `get pods` | grants `get pods` | Request succeeds |
| denies `delete pods` | grants `delete pods` | Rejected at the control plane (`403`, audited `denied`); agent never called |
| allows `delete pods` | no binding | Kubernetes refuses: `User "kbridge:<email>" cannot delete resource "pods"` |
| allows `create pods` | grants `create pods` | Admission still applies as the user, e.g. PodSecurity `baseline` refuses a `hostPath` pod |

---

## Binding kbridge users in the cluster

Each kbridge user appears to Kubernetes as the user `kbridge:<email>` (with the
group `system:authenticated`). The agent binds those users as the policy's
`clusterAccess` says (see [rbac.md](rbac.md#cluster-bindings-clusteraccess)):
it creates `kbridge-<role>-<clusterRole>` bindings labelled
`app.kubernetes.io/managed-by=kbridge`, puts them back when someone edits them,
and deletes them when policy stops asking for them. `kb admin rbac status`
shows whether each cluster matches.

The agent chart's `accessCeiling` is the most the agent may hand out:

```yaml
accessCeiling:
  clusterRoles: [view, edit, cluster-admin]  # ClusterRoles the agent may bind
  namespaces: []               # empty = any namespace except the protected ones
  clusterWide: true            # allow ClusterRoleBindings
```

These are the defaults, so a fresh install works with the default policy
(`admin` is bound to `cluster-admin`, `viewer` to `view`, cluster-wide). A
cluster owner can lower the ceiling, for example to `[view, edit]` with
`clusterWide: false`, to limit what any kbridge policy can grant there.

The agent's ServiceAccount gets `bind` on those ClusterRoles only, write
access to RoleBindings (cluster-wide, or in the listed namespaces), write
access to ClusterRoleBindings only with `clusterWide`, and list and watch on
both kinds everywhere. Kubernetes enforces this: a policy that asks for more
gets a `403` for that binding, shown in `kb admin rbac status`, and the other
bindings still apply. The agent, not Kubernetes, refuses the release
namespace and `protectedNamespaces`, bindings not named `kbridge-`, and
subjects that are not `kbridge:` users. The chart refuses to render with an
empty `clusterRoles` or with the release namespace in `namespaces`.

A user with no binding can still reach kbridge but every request is refused
by the API server. `kb auth whoami` prints the name Kubernetes sees. Bindings
you make by hand for `kbridge:` users still work, and the agent leaves any
binding without its label alone.

### Kubernetes audit log

Requests made through kbridge appear in the API server audit log with:

- `user.username`: the agent's ServiceAccount,
  `system:serviceaccount:<release namespace>:<release>-agent`;
- `impersonatedUser.username`: `kbridge:<email>`, the user RBAC was evaluated for;
- `impersonatedUser.extra.kbridge-request-id`: the id the control plane gave the
  command, stream or port-forward session.

The request id is the one in the control plane log line
`Queued command <id> for cluster <name>`. The kbridge audit row (`kb admin
audit`) records the user email, cluster, command and time but not the id, so
match it to the Kubernetes audit event by email, cluster and time.

## Known limits

- **A compromised control plane can bind the ceiling roles to anyone** in any
  namespace that is not protected: it decides who holds which role, and it
  can name any `kbridge:` user and send commands as that user. The agent only
  checks the shape of each binding (name, namespace, `kbridge:` users), and
  `accessCeiling` caps what can be bound. The ceiling scopes what kbridge may
  hand out in a cluster; it is not a boundary against a compromised control
  plane: once any user is bound to `cluster-admin` through kbridge, a
  compromised control plane can act as that user by impersonation.
- **A stolen agent token can impersonate any user name.** Kubernetes cannot
  limit `users impersonate` to the `kbridge:` prefix, so the agent's
  credential reaches every binding whose subject is a `User`, `kbridge:` or
  not. Bind people outside kbridge by group.
- **`edit` lets users run pods as that namespace's ServiceAccounts**, and so
  use whatever those ServiceAccounts may do. Grant `edit` only where the
  namespace's ServiceAccounts hold no more than the user should.
- **kubectl runs as the agent's uid.** The connection and file flag checks are
  what keep a command from reading the agent's token file. Running kubectl
  under a second uid is the planned fix.
- **kbridge's own audit log is editable** by whoever can reach the control
  plane's host or database. The Kubernetes audit log records the same
  requests under `impersonatedUser`.
- **Per-call agent authentication is the agent id issued at registration.**
  Mutual TLS is planned before a stable release.

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
