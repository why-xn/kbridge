# Admin Guide

Administrative operations (`kb admin …` and `/api/v1/admin/*`) require the
admin account the control plane seeds from `auth.admin_*` in
`control-plane.yaml` on first start. The admin flag lives on that user record,
and there is no command or API call that grants it to another user yet. The
`admin` role in the [RBAC policy](rbac.md) is a different thing: it decides
which kubectl commands a user may run, not who may administer kbridge.

These tasks are driven primarily through the `kb admin …` CLI (run
`kb login` as an admin first); the equivalent HTTP API is shown as a
secondary option. See the [CLI reference](cli.md) and [API reference](api.md).

## Users

Users authenticate to the control plane; their permissions come from the policy file
(matched by email), so there is no role-assignment API — edit the policy
`bindings` instead.

```bash
kb admin users list
kb admin users create --email dev@corp.com --name "Dev User"
```

Or via the API:

```bash
curl -X POST https://control-plane:8080/api/v1/admin/users \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"email":"dev@corp.com","name":"Dev User","password":"..."}'
```

Passwords are bcrypt-hashed and never returned. Update name/active/password with
`PUT /api/v1/admin/users/{id}`; remove with `DELETE`.

## Agent tokens

Each agent registers with a token bound to exactly one cluster. Generate one
with the CLI, then set it as the agent's `control_plane.token` (the plaintext is shown
only once):

```bash
kb admin agent-tokens create --cluster prod-us-east --description "prod agent" --expires-in-days 90
kb admin agent-tokens list --cluster prod-us-east   # prefixes, never the secret
kb admin agent-tokens revoke <id>
```

Tokens are stored only as an HMAC-SHA256 digest (keyed by `auth.token_pepper`,
which falls back to `jwt_secret`) — never in plaintext. The CLI's `list` shows
each token's ID, cluster, prefix, revoked flag and description. The API's
`GET /api/v1/admin/agent-tokens` also returns `last_used_at`, updated on every
successful registration, and `expires_at`; a token that is old and never used
is a good candidate to revoke.

Creating a token also registers its cluster: `kb clusters list` shows it as
`disconnected` until an agent connects with the token.

Via the API instead:

```bash
curl -X POST https://control-plane:8080/api/v1/admin/agent-tokens \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"cluster_name":"prod-us-east","description":"prod agent","expires_in_days":90}'
# -> {"id":"...","token":"kbat_...","cluster_name":"prod-us-east",...}
# list:   GET    /api/v1/admin/agent-tokens[?cluster=<name>]
# revoke: DELETE /api/v1/admin/agent-tokens/{id}
```

**Revocation is immediate.** Revoking a token disconnects every agent using it
at once: its queued commands fail with `agent is disconnected`, `kb clusters
list` shows the cluster `disconnected`, and the agent cannot register again, so
its pod stops (CrashLoopBackOff) until it is given a new token. A dead agent
shows `disconnected` within seconds too; see
[operations](operations.md#agent).

**Rotation:** issue a new token, update the agent, then revoke the old one. In
that order nothing is cut off: the agent already runs on the new token. The
update rolls the agent pod; the new pod takes over the cluster as soon as it
registers, and the old one stands by until Kubernetes stops it, finishing any
command it was running within the pod's termination grace period (see
[operations](operations.md#agent-token-rotation)). A stream it was serving (`logs -f`, `exec -it`,
`port-forward`) ends when the old pod stops; run it again.

For local development you can instead seed a token via the control plane's `bootstrap`
config (see [configuration](configuration.md)).

## Audit logs

Every command attempt is recorded (status `success`, `failed`, `denied`, or
`canceled`) with user, cluster, command, namespace, exit code, duration, and
client IP. A command refused because the cluster's agent is disconnected is
recorded as `failed` with the error `agent is disconnected` and no exit code;
one that hits its timeout is `failed` with exit code `-1`. The CLI table shows
time, user, cluster, status, exit code and command; the namespace, duration,
client IP and error message are in the API response only.

```bash
kb admin audit --cluster prod --status denied --limit 100
```

Or `GET /api/v1/admin/audit` with `user`, `cluster`, `status`, `from`/`to`
(RFC3339), `page`, `per_page` filters.

Logs older than `audit.retention_days` are pruned automatically every
`audit.cleanup_interval` (configured in `control-plane.yaml`).

## RBAC changes

Edit the policy file referenced by `rbac.policy_file`. The control plane reloads it
within 10 s of the file changing, or at once on `SIGHUP`
(`kill -HUP <control-plane-pid>`). With the Helm chart, change `rbac.policy` and
run `helm upgrade`; the control plane restarts with the new policy. A reload
of the file that fails to parse is rejected and the previous policy stays
active (the error is logged). Through Helm there is no previous policy to
keep: the new pod fails to start and logs the error, so fix the value and
upgrade again. In a pod, send the signal with
`kubectl -n kbridge exec deploy/kbridge-control-plane -- kill -HUP 1`.
See the [RBAC reference](rbac.md#reloading). Run
`kb admin rbac status` to see whether each cluster's RoleBindings match the policy
(see [operations](operations.md)).
