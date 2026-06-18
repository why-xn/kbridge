# CLI Reference

The `kbridge` CLI talks to the central service over REST. Configuration lives in
`~/.kbridge/config.yaml` (see [configuration](configuration.md)).

## Authentication

### `kbridge login`
Prompts for the central URL (if unset), email, and password; stores the access
and refresh tokens.

```bash
kbridge login
```

### `kbridge logout`
Invalidates the refresh token on the server and clears the local token.

## Clusters

### `kbridge clusters list` (alias `ls`)
Lists clusters registered with central and their status.

```bash
kbridge clusters list
```

### `kbridge clusters use <name>`
Sets the active cluster for subsequent `kubectl` commands.

```bash
kbridge clusters use dev-cluster
```

## Running commands

### `kbridge kubectl <args...>` (alias `k`)
Runs a kubectl command on the active cluster via central. Standard kubectl
syntax and flags work; access is checked against the RBAC policy.

```bash
kbridge kubectl get pods -A
kbridge kubectl logs deploy/api -n prod
kbridge kubectl edit configmap app-config
kbridge k get nodes
```

**Streaming (`logs -f` / `get -w`).** When the command uses a follow/watch flag
(`-f`, `--follow`, `-w`, `--watch`), the CLI streams output live until you stop
it with Ctrl-C — no special syntax needed:

```bash
kbridge kubectl logs -f deploy/api -n prod    # tail logs live
kbridge kubectl get pods -w                   # watch resource changes
```

### `kbridge status`
Shows the current central URL, authenticated user, and active cluster.

## Admin (requires the admin role)

### `kbridge admin users list` (alias `ls`)
Lists all users.

### `kbridge admin users create`
Creates a user. Prompts for the password if `--password` is omitted.

```bash
kbridge admin users create --email dev@corp.com --name "Dev User"
kbridge admin users create --email ci@corp.com --name CI --password "$TOKEN"
```

| Flag | Description |
|------|-------------|
| `--email` | User email (required) |
| `--name` | Display name (required) |
| `--password` | Password (prompted if omitted) |

### `kbridge admin agent-tokens` (alias `tokens`)
Manage the tokens agents use to register. Subcommands: `create`, `list`, `revoke`.

```bash
# Generate a token for a cluster (printed once — set it as the agent's central.token)
kbridge admin agent-tokens create --cluster prod-us-east --description "prod agent"
kbridge admin agent-tokens create --cluster dev --expires-in-days 90

# List tokens (shows the prefix, never the secret)
kbridge admin agent-tokens list
kbridge admin agent-tokens list --cluster prod-us-east

# Revoke a token by ID
kbridge admin agent-tokens revoke <id>
```

| `create` flag | Description |
|---------------|-------------|
| `--cluster` | Cluster the token is bound to (required) |
| `--description` | Optional description |
| `--expires-in-days` | Optional expiry in days (0 = no expiry) |

### `kbridge admin audit`
Shows the command audit log, newest first.

```bash
kbridge admin audit
kbridge admin audit --user dev@corp.com --status denied
kbridge admin audit --cluster prod --limit 100
```

| Flag | Description | Default |
|------|-------------|---------|
| `--user` | Filter by user email | — |
| `--cluster` | Filter by cluster name | — |
| `--status` | `success` / `failed` / `denied` / `timeout` | — |
| `--limit` | Max entries | 50 |

## Global behaviour

- A `401` response means your token expired — run `kbridge login` again.
- Permission errors (`403`) come from the RBAC policy; see [rbac.md](rbac.md).
