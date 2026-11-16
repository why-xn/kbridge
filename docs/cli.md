# CLI Reference

The `kb` CLI talks to the control plane over REST. Configuration lives in
`~/.kbridge/config.yaml` (see [configuration](configuration.md)). The binary is
also installed as `kbridge` (a symlink) for back-compat.

**kubectl by default.** The first argument decides what runs: the management
commands `login`, `logout`, `status`, `clusters` (alias `cluster`), and `admin`
run locally; **anything else is sent to kubectl** on the active cluster. So
`kb get pods` runs kubectl, while `kb admin users list` runs the admin command.
Use `kb kubectl …` (or `kb k …`) to force kubectl when a name would otherwise
collide.

## Authentication

### `kb login`
Prompts for the control plane URL (if unset), email, and password; stores the access
and refresh tokens.

```bash
kb login
```

### `kb logout`
Invalidates the refresh token on the server and clears the local token.

## Clusters

### `kb clusters list` (alias `ls`)
Lists every cluster the control plane knows, sorted by name, and its status.
A cluster with no agent registered (for example right after a control plane
restart, until its agent reconnects) shows `disconnected`.

```bash
kb clusters list
```

### `kb clusters use <name>`
Sets the active cluster for subsequent `kubectl` commands.

```bash
kb clusters use dev-cluster
```

## Running commands

### `kb <args...>` (explicit: `kb kubectl …` / `kb k …`)
Runs a kubectl command on the active cluster via the control plane. Standard kubectl
syntax and flags work; access is checked against the RBAC policy. No `kubectl`
keyword is needed — it's the default for any non-management command.

```bash
kb get pods -A
kb logs deploy/api -n prod
kb edit configmap app-config
kb get nodes
```

**Editing (`kb edit`).** The editor comes from `KUBE_EDITOR`, `EDITOR` or
`VISUAL` (default `vi`). It may take arguments (`KUBE_EDITOR="code --wait"`).
As with kubectl, a value with a quote or backslash is run by `sh`, so shell
quoting works (`KUBE_EDITOR='sed -i s/replicas:\ 1/replicas:\ 2/'`); any other
value is split on spaces. On Windows the value is always split on spaces.
`kb edit` reads the object, opens it, and saves it with `apply`, so the audit
log records it as `apply -f -`, kubectl may warn about a missing
`last-applied-configuration` annotation the first time, and when something
else changed the object meanwhile the save fails with a `Conflict`: run
`kb edit` again.

**Local manifests (`-f`).** kubectl runs on the agent, so the CLI reads local
`-f` inputs — files, directories (`-R` to recurse) and `-` (stdin) — and sends
their contents along. `http(s)` URLs are fetched by the CLI (10 MiB limit).
Kustomize (`-k`) is not supported; pipe it instead:

```bash
kb apply -f app.yaml
kb apply -R -f ./manifests
kubectl kustomize ./overlay | kb apply -f -
```

**Default namespace.** Without `-n`, commands run in the `default` namespace, as
with plain kubectl.

**Streaming (`logs -f` / `get -w`).** When the command uses a follow/watch flag
(`logs -f`/`--follow`, or `-w`/`--watch` on any command), the CLI streams output
live until you stop it with Ctrl-C and exits with kubectl's exit code:

```bash
kb logs -f deploy/api -n prod    # tail logs live
kb get pods -w                   # watch resource changes
```

**Interactive exec (`exec -it`).** `kb exec` opens an interactive session on
a pod. Use `-it` for a full TTY (terminal resize, full-screen apps like `vim`
or `htop`); use `-i` alone for stdin-only (no TTY). Both go through the
interactive path. Without `-i` or `-t`, `kb exec` falls back to the one-shot
path (same as `kb exec`). The `--` separator is required to pass flags to the
remote command instead of to `kb` itself. Ctrl-C and Ctrl-D are forwarded to
the remote shell, not to the CLI. There is no inactivity timeout on the session.
In a script or a pipe, where stdin is not a terminal, use `-i` alone: `-it`
without a terminal waits forever.

```bash
kb exec -it <pod> -- sh                          # interactive shell (full TTY)
kb exec -it <pod> -c <container> -- bash         # specific container
kb exec -it <pod> -n <namespace> -- sh           # specific namespace
kb exec -i <pod> -- sh -c "cat /etc/hosts"       # stdin, no TTY
```

### `kb port-forward <pod> [LOCAL:REMOTE ...]`
Opens local TCP listeners that tunnel to the pod's ports through the control plane. Each
port spec can be `LOCAL:REMOTE` (fixed local port), bare `REMOTE` (local equals
remote), or `:REMOTE` (OS-assigned random local port). Multiple ports can be
forwarded in a single command, each remote port once. Any TCP-based protocol works (Postgres, Redis,
HTTP, etc.). Binds localhost only (v1). Press Ctrl-C to stop all listeners. There
is no inactivity timeout — the session stays open until you stop it.

```bash
kb port-forward deploy/db 5432:5432              # fixed local port
kb port-forward deploy/redis 6379                # local = remote
kb port-forward deploy/db :5432 :6379            # random local ports
kb port-forward deploy/db 5432:5432 -n prod      # specific namespace
kb port-forward deploy/db 5432:5432 6379:6379    # multiple ports at once
```

### `kb status`
Shows the control plane URL, whether it answers, and the selected cluster with
its status. It does not show who you are, and after `kb logout` it still
reports the control plane as reachable; a command that answers
`authentication required` is the cue to run `kb login`.

## Admin (requires the admin role)

### `kb admin users list` (alias `ls`)
Lists all users.

### `kb admin users create`
Creates a user. Prompts for the password if `--password` is omitted.

```bash
kb admin users create --email dev@corp.com --name "Dev User"
kb admin users create --email ci@corp.com --name CI --password "$TOKEN"
```

| Flag | Description |
|------|-------------|
| `--email` | User email (required) |
| `--name` | Display name (required) |
| `--password` | Password (prompted if omitted) |

### `kb admin agent-tokens` (alias `tokens`)
Manage the tokens agents use to register. Subcommands: `create`, `list`, `revoke`.

```bash
# Generate a token for a cluster (printed once — set it as the agent's control_plane.token)
kb admin agent-tokens create --cluster prod-us-east --description "prod agent"
kb admin agent-tokens create --cluster dev --expires-in-days 90

# List tokens (shows the prefix, never the secret)
kb admin agent-tokens list
kb admin agent-tokens list --cluster prod-us-east

# Revoke a token by ID
kb admin agent-tokens revoke <id>
```

| `create` flag | Description |
|---------------|-------------|
| `--cluster` | Cluster the token is bound to (required) |
| `--description` | Optional description |
| `--expires-in-days` | Optional expiry in days (0 = no expiry) |

### `kb admin audit`
Shows the command audit log, newest first.

```bash
kb admin audit
kb admin audit --user dev@corp.com --status denied
kb admin audit --cluster prod --limit 100
```

| Flag | Description | Default |
|------|-------------|---------|
| `--user` | Filter by user email | — |
| `--cluster` | Filter by cluster name | — |
| `--status` | `success` / `failed` / `denied` / `canceled` | — |
| `--limit` | Max entries | 50 |

A command that hits its timeout is recorded as `failed` with exit code `-1`.
The table shows time, user, cluster, status, exit code and command; the
namespace, duration and client IP are in the API response only.

### `kb admin rbac status`
Shows, per cluster, whether the RoleBindings the agent manages match the
policy's `clusterAccess`: columns `CLUSTER`, `VERSION` (hash of the desired
bindings), `STATE` (`in sync` or `drift`), `ERRORS` and `REPORTED` (when the
agent last reported). Each error is listed under its row with the command that
fixes it, for example the Pod Security label a namespace is missing. See
[rbac.md](rbac.md#cluster-bindings-clusteraccess).

## Global behaviour

- A `401` response means your token expired — run `kb login` again.
- Permission errors (`403`) come from the RBAC policy; see [rbac.md](rbac.md).
