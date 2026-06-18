# Admin Guide

Administrative operations require a user with the `admin` role (granted via the
[RBAC policy](rbac.md) bindings). The first admin is seeded from `auth.admin_*`
in `central.yaml` on first startup.

These tasks are driven primarily through the `kb admin …` CLI (run
`kb login` as an admin first); the equivalent HTTP API is shown as a
secondary option. See the [CLI reference](cli.md) and [API reference](api.md).

## Users

Users authenticate to central; their permissions come from the policy file
(matched by email), so there is no role-assignment API — edit the policy
`bindings` instead.

```bash
kb admin users list
kb admin users create --email dev@corp.com --name "Dev User"
```

Or via the API:

```bash
curl -X POST https://central:8080/api/v1/admin/users \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"dev@corp.com","name":"Dev User","password":"..."}'
```

Passwords are bcrypt-hashed and never returned. Update name/active/password with
`PUT /api/v1/admin/users/{id}`; remove with `DELETE`.

## Agent tokens

Each agent registers with a token bound to exactly one cluster. Generate one
with the CLI, then set it as the agent's `central.token` (the plaintext is shown
only once):

```bash
kb admin agent-tokens create --cluster prod-us-east --description "prod agent" --expires-in-days 90
kb admin agent-tokens list --cluster prod-us-east   # prefixes + last_used_at, never the secret
kb admin agent-tokens revoke <id>
```

Tokens are stored only as an HMAC-SHA256 digest (keyed by `auth.token_pepper`,
which falls back to `jwt_secret`) — never in plaintext. `list` reports each
token's `last_used_at`, updated on every successful registration; a token that
is old and never used is a good candidate to revoke.

Via the API instead:

```bash
curl -X POST https://central:8080/api/v1/admin/agent-tokens \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"cluster_name":"prod-us-east","description":"prod agent","expires_in_days":90}'
# -> {"id":"...","token":"kbat_...","cluster_name":"prod-us-east",...}
# list:   GET    /api/v1/admin/agent-tokens[?cluster=<name>]
# revoke: DELETE /api/v1/admin/agent-tokens/{id}
```

**Rotation:** issue a new token, update the agent, then revoke the old one. The
agent re-registers on its next connection.

For local development you can instead seed a token via central's `bootstrap`
config (see [configuration](configuration.md)).

## Audit logs

Every command attempt is recorded (status `success`, `failed`, `denied`, or
`timeout`) with user, cluster, command, namespace, exit code, duration, and
client IP.

```bash
kb admin audit --cluster prod --status denied --limit 100
```

Or `GET /api/v1/admin/audit` with `user`, `cluster`, `status`, `from`/`to`
(RFC3339), `page`, `per_page` filters.

Logs older than `audit.retention_days` are pruned automatically every
`audit.cleanup_interval` (configured in `central.yaml`).

## RBAC changes

Edit the policy file referenced by `rbac.policy_file`. Central reloads it
automatically (file watch) or on demand via `SIGHUP`
(`kill -HUP <central-pid>`) — use the latter where the filesystem doesn't
deliver change events. A policy that fails to parse is rejected and the previous
one stays active (the error is logged). See the [RBAC reference](rbac.md#reloading).
