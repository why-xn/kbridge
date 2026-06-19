# CLI Reference

The `kb` CLI talks to the central service over REST. Configuration lives in
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
Prompts for the central URL (if unset), email, and password; stores the access
and refresh tokens.

```bash
kb login
```

### `kb logout`
Invalidates the refresh token on the server and clears the local token.

## Clusters

### `kb clusters list` (alias `ls`)
Lists clusters registered with central and their status.

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
Runs a kubectl command on the active cluster via central. Standard kubectl
syntax and flags work; access is checked against the RBAC policy. No `kubectl`
keyword is needed — it's the default for any non-management command.

```bash
kb get pods -A
kb logs deploy/api -n prod
kb edit configmap app-config
kb get nodes
```

**Streaming (`logs -f` / `get -w`).** When the command uses a follow/watch flag
(`-f`, `--follow`, `-w`, `--watch`), the CLI streams output live until you stop
it with Ctrl-C — no special syntax needed:

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

```bash
kb exec -it <pod> -- sh                          # interactive shell (full TTY)
kb exec -it <pod> -c <container> -- bash         # specific container
kb exec -it <pod> -n <namespace> -- sh           # specific namespace
kb exec -i <pod> -- sh -c "cat /etc/hosts"       # stdin, no TTY
```

### `kb status`
Shows the current central URL, authenticated user, and active cluster.

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
# Generate a token for a cluster (printed once — set it as the agent's central.token)
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
| `--status` | `success` / `failed` / `denied` / `timeout` | — |
| `--limit` | Max entries | 50 |

## Global behaviour

- A `401` response means your token expired — run `kb login` again.
- Permission errors (`403`) come from the RBAC policy; see [rbac.md](rbac.md).
