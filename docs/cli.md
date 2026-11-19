# CLI Reference

The `kb` CLI talks to the control plane over REST. Configuration lives in
`~/.kbridge/config.yaml` (see [configuration](configuration.md)). `make build`
also creates a `kbridge` symlink to it; the installer installs `kb` only.

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

### Change your password
There is no `kb` command for it yet; call the API with the access token
`kb login` saved in `~/.kbridge/config.yaml`. Do it after your first login if
an admin chose your password:

```bash
TOKEN=$(awk '/^token:/{print $2}' ~/.kbridge/config.yaml)
curl -X POST https://<control-plane>/api/v1/auth/change-password \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"current_password":"<pw>","new_password":"<new password>"}'
# {"message":"password changed"}
```

A `401` means the access token expired: run `kb login` and try again.

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
with plain kubectl. Only kubectl's own `-n`/`--namespace` counts, in any form
(`-n x`, `-nx`, `--namespace=x`), and only before `--`: in
`kb exec web -- grep -n web /f` the namespace is `default`.

**One-shot commands.** A command without a follow/watch flag and without
`exec -i`/`-it` is one-shot: the agent runs it and `kb` prints the output when
it ends. It may run for up to 5 minutes; then the agent stops it and `kb`
exits with status 1. The agent runs one-shot commands for a cluster one at a
time, so while one runs, every other user's one-shot command on that cluster
waits. Stopping `kb` (Ctrl-C) does not stop the command on the agent. Run
anything that may not end quickly, such as a `sleep`, a migration or a
`tail -f`, with `kb exec -i`.

**`-h` and `--help` belong to `kb`.** Even after `--`, `kb` takes them and
prints its own help: `kb exec web -- ls -h /` shows `kb` help. Wrap the
command in a shell to pass them on: `kb exec web -- sh -c 'ls -h /'`.

**Streaming (`logs -f` / `get -w`).** When the command uses a follow/watch flag
(`logs -f`/`--follow`, or `-w`/`--watch` on any command), the CLI streams output
live until you stop it with Ctrl-C and exits with kubectl's exit code. An
HTTP proxy in front of the control plane cuts a stream that is quiet for its
idle timeout; see [production install](production-install.md#why-not-an-http-ingress):

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
the remote shell, not to the CLI. kbridge sets no inactivity timeout on the
session; a proxy in front of the control plane may (see
[production install](production-install.md#why-not-an-http-ingress)).
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
forwarded in a single command. List each remote port once: a remote port
listed twice makes the command hang. Any TCP-based protocol works (Postgres, Redis,
HTTP, etc.). Binds localhost only (v1). Press Ctrl-C to stop all listeners.
kbridge sets no inactivity timeout, so the session stays open until you stop
it; a proxy in front of the control plane may set one (see
[production install](production-install.md#why-not-an-http-ingress)).

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

## When a command is refused

A refusal comes from kbridge's policy, from the cluster's RBAC, or from a
rule kbridge applies to everyone. The error text tells you which.

**kbridge's policy** refused it before it reached the cluster:

```text
Error: permission denied: delete pod in namespace "kube-system" on cluster "prod-us-east" is not allowed for dev@example.com; ask a kbridge admin to extend your role
```

Your role does not allow that verb, resource or namespace. Ask a kbridge
admin to extend your role, and give them the message.

**The cluster's RBAC** refused it after kbridge allowed it:

```text
Error from server (Forbidden): pods "x" is forbidden: User "kbridge:dev@example.com" cannot delete resource "pods" in API group "" in the namespace "default"
```

Your role allows it, but the cluster has no binding that lets
`kbridge:<your email>` do it there. Ask the admin to add `clusterAccess` for
that namespace to your role.

**A rule kbridge applies to everyone** also starts with
`Error from server (Forbidden)`, but says the namespace `is reserved for the
kbridge agent` or that the request `is not allowed through kbridge`:

```text
Error from server (Forbidden): reading secrets across all namespaces is not allowed through kbridge (get secrets)
```

No role change unblocks these; see the list below.

Before you ask, check three things:

- **Type the resource the way your role lists it.** `resources: ["pods"]`
  admits `kb get pods`, not `kb get pod` or `kb get po`. The error repeats the
  word you typed.
- **Put flags after the resource**, or write a value flag as `--flag=value`.
  Before the resource, kbridge takes the word after a flag it does not know,
  even a true/false flag such as `--no-headers`, as that flag's value:
  `kb get --no-headers pods` has no resource, and a role that lists resources
  refuses it. See
  [rbac.md](rbac.md#how-a-kubectl-command-maps-to-a-request).
- **See who the cluster thinks you are** (the default `viewer` role allows
  it):

  ```bash
  kb auth whoami
  # ATTRIBUTE                   VALUE
  # Username                    kbridge:dev@example.com
  # Groups                      [system:authenticated]
  # Extra: kbridge-request-id   [req-3fd7b2927b14dc01]
  ```

Some commands kbridge refuses for everyone, whatever the role: connection and
file flags such as `--server` or `--kubeconfig`, verbs such as `cp`, `proxy`
and `config`, and anything in the agent's own namespace. See
[security.md](security.md#layer-2--the-agent-what-kubectl-can-actually-do)
for the full list. Admins: [rbac.md](rbac.md) says how a command maps to a
rule and how to add `clusterAccess`.

## Admin (requires the kbridge admin account)

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
| `--status` | `success` / `failed` / `denied` / `timeout` / `canceled` | — |
| `--limit` | Max entries | 50 |

A command that hits its timeout is recorded as `failed` with exit code `-1`.
`timeout` means the agent did not answer within the command's timeout plus
5 s.
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
- Permission errors (`403`) come from the RBAC policy; see [When a command is refused](#when-a-command-is-refused).
