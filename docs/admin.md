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

Admins can also see clusters, the audit log, agent tokens, and users in the
read-only [admin console](console.md) at `https://<control-plane>/`.

## Onboard a user

Five steps take a new person from nothing to running commands.

1. **Create the user.** Choose a first password and send it to them
   privately; step 5 shows how they change it.

   ```bash
   kb admin users create --email dev@example.com --name Dev --password <pw>
   ```

   Until a binding names them, they hold the policy's `default` role
   (`viewer` in the default policy): read-only.

2. **Give them a role.** Add a binding to the [policy](rbac.md) for a role
   it defines (the default policy has only `admin` and `viewer`; the
   [RBAC example](rbac.md#example) adds a `developer` role; set its
   `clusters` to yours, since the example's `dev-*` and `staging` match no
   other cluster). A bound user
   holds only the roles listed, so list `viewer` too if they should keep it:

   ```yaml
   bindings:
     - subject: admin@kbridge.local   # your auth.adminEmail
       roles: ["admin"]
     - subject: dev@example.com
       roles: ["viewer", "developer"]
   ```

   With the Helm chart, edit `rbac.policy` in your values file. It replaces
   the whole policy, so start from the one in the chart's
   [values.yaml](../charts/control-plane/values.yaml) and keep the admin's
   binding. Then upgrade:

   ```bash
   helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
     -n kbridge --version 0.3.1-alpha.1 --reuse-values -f values-control-plane.yaml
   ```

   `--reuse-values` keeps what you set with `--set` at install, such as
   `auth.jwtSecret`; without it those go back to the chart defaults. The
   upgrade restarts the control plane, so `kb` fails for about a minute. If
   you reach it through `kubectl port-forward`, as in the quick start, the
   forward ends with the old pod: start it again once the new pod is ready.

   Without Helm, edit the policy file (or the ConfigMap it is mounted from).
   The control plane reloads it within 10 s; see
   [Reloading](rbac.md#reloading).

3. **Create and label the namespace, if the role binds one.** Every
   namespace in a role's `clusterAccess` must exist in the target cluster; the
   agent does not create it. A missing one shows in `kb admin rbac status` as
   `reading namespace "<ns>": ... not found` (or, for `view`,
   `... namespaces "<ns>" not found`): create it with
   `kubectl create namespace <ns>`. A namespace where the role binds `edit`
   also needs a Pod Security label; see
   [installation step 4](installation.md#step-4-label-namespaces-where-kbridge-grants-edit).

4. **Wait for `in sync`.** A new user's bindings, and a new role's, appear
   on the agent's next sync, within the agent chart's `sync.interval` (60 s
   by default). Until then the API server refuses their requests, even the
   reads the `default` role allows, and the row shows `drift`:

   ```bash
   kb admin rbac status
   # CLUSTER       VERSION   STATE    ERRORS  REPORTED
   # prod-us-east  84d5df48  in sync  0       2026-11-19T04:23:33Z
   ```

   An error under the row names the command that fixes it, for example the
   missing Pod Security label.

5. **Send them the control plane URL**, their email and first password, and
   point them at [You have a kbridge account](../README.md#you-have-a-kbridge-account)
   in the README, which tells them to
   [change their password](cli.md#change-your-password) after the first login.

Next, for you: [Operations](operations.md) for backup, upgrades and
troubleshooting.

## Users

Permissions come from the policy file, matched by email, so there is no
role-assignment API; see [Onboard a user](#onboard-a-user).

```bash
kb admin users list
```

To create a user through the API instead:

```bash
curl -X POST https://<control-plane>/api/v1/admin/users \
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
successful registration, and `expires_at`; the console's Agent Tokens screen
shows both too. A token that is old and never used
is a good candidate to revoke.

Creating a token also registers its cluster: `kb clusters list` shows it as
`disconnected` until an agent connects with the token.

Via the API instead:

```bash
curl -X POST https://<control-plane>/api/v1/admin/agent-tokens \
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

Every command attempt is recorded (status `success`, `failed`, `denied`,
`timeout`, or `canceled`) with user, cluster, command, namespace, exit code, duration, and
client IP. The namespace is the one kbridge enforced: the command's
`-n`/`--namespace`, else `default`, and `*` for `-A`. A command refused because
the cluster's agent is disconnected is recorded as `failed` with the error
`agent is disconnected` and no exit code; one that hits its timeout is `failed`
with exit code `-1`, and one the agent did not answer within its timeout plus
5 s is `timeout`. The CLI table shows time, user, cluster, status, exit code
and command; the namespace, duration, client IP and error message are in the
API response only.

```bash
kb admin audit --cluster prod --status denied --limit 100
```

Or `GET /api/v1/admin/audit` with `user`, `cluster`, `status`, `namespace`,
`command` (substring), `from`/`to` (RFC3339), `page`, `per_page` filters.

Logs older than `audit.retention_days` are pruned automatically every
`audit.cleanup_interval` (configured in `control-plane.yaml`).

## RBAC changes

Edit the policy file referenced by `rbac.policy_file`. The control plane reloads it
within 10 s of the file changing, or at once on `SIGHUP`
(`kill -HUP <control-plane-pid>`). With the Helm chart, change `rbac.policy` and
run `helm upgrade` as in [Onboard a user](#onboard-a-user); the control plane
restarts with the new policy. A reload
of the file that fails to parse is rejected and the previous policy stays
active (the error is logged). Through Helm there is no previous policy to
keep: the new pod fails to start and logs the error, so fix the value and
upgrade again. In a pod, send the signal with
`kubectl -n kbridge exec deploy/kbridge-control-plane -- kill -HUP 1`.
See the [RBAC reference](rbac.md#reloading). Run
`kb admin rbac status` to see whether each cluster's RoleBindings match the policy
(see [operations](operations.md)).
