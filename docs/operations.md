# Operations Guide

Day-2 operations for kbridge: backup, upgrades, token rotation, troubleshooting,
and known limitations.

---

## Backup & Restore (SQLite)

Backup and restore stop the control plane for a moment and work through a
helper pod that mounts its PVC (why: [below](#why-the-control-plane-stops)).
The commands assume the release `kbridge-control-plane` in namespace
`kbridge`.

### Helper pod

```bash
kubectl -n kbridge scale deploy/kbridge-control-plane --replicas=0
# The old pod keeps the database open until it is gone; wait for it.
kubectl -n kbridge wait --for=delete pod \
  -l app.kubernetes.io/name=kbridge-control-plane,app.kubernetes.io/instance=kbridge-control-plane \
  --timeout=120s
kubectl -n kbridge apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: kbridge-db
spec:
  securityContext:
    runAsNonRoot: true
    runAsUser: 10001
    fsGroup: 10001
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: db
      image: alpine:3.20
      command: ["sleep", "3600"]
      securityContext:
        allowPrivilegeEscalation: false
        capabilities: {drop: ["ALL"]}
      volumeMounts:
        - {name: data, mountPath: /data}
  volumes:
    - name: data
      persistentVolumeClaim: {claimName: kbridge-control-plane-data}
EOF
kubectl -n kbridge wait --for=condition=Ready pod/kbridge-db
```

Charts before standard Helm names called the PVC `<release>-control-plane-data`;
`kubectl -n kbridge get pvc` shows yours.

### Backup

```bash
# Start the helper pod (above), then:
kubectl -n kbridge exec kbridge-db -- sh -c 'cd /data && tar -cf - kbridge.db*' \
  > kbridge-backup-$(date +%Y%m%d).tar
kubectl -n kbridge delete pod kbridge-db
kubectl -n kbridge scale deploy/kbridge-control-plane --replicas=1
```

### Restore

```bash
# Start the helper pod (above), then restore the archive the backup step wrote.
# The redirect is checked before anything runs, so a wrong path deletes nothing.
BACKUP=kbridge-backup-20260115.tar
kubectl -n kbridge exec -i kbridge-db -- \
  sh -c 'rm -f /data/kbridge.db /data/kbridge.db-wal /data/kbridge.db-shm && tar -C /data -xf -' < "$BACKUP"
kubectl -n kbridge delete pod kbridge-db
kubectl -n kbridge scale deploy/kbridge-control-plane --replicas=1
```

### Why the control plane stops

The control plane stores all state in a single SQLite file at `/data/kbridge.db`
(the path set by `persistence.dbPath` in the Helm chart, defaulting to
`/data/kbridge.db`). The image has no `sqlite3`, and a copy of a live WAL-mode
database is not consistent, so the database is copied while no control plane
has it open.

### PVC snapshots

If your storage class supports `VolumeSnapshot`, take a PVC snapshot for
point-in-time cluster-level recovery before destructive changes:

```bash
kubectl -n kbridge apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: kbridge-control-plane-data-$(date +%Y%m%d)
spec:
  source:
    persistentVolumeClaimName: kbridge-control-plane-data
EOF
```

---

## Upgrades

### Helm upgrade

[Back up](#backup) first, then:

```bash
# Production install:
helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.1-alpha.1 \
  --reuse-values -f values-control-plane.yaml

# Quick start (installed with --set only):
helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.1-alpha.1 \
  --reuse-values
```

`--reuse-values` keeps what was set with `--set`; the
[production install](production-install.md) also keeps a values file, which
every upgrade passes again. The chart's `--version` and its default
`image.tag` move together, so pin the chart version rather than overriding the
image tag. From a clone, use `./charts/control-plane` in place of the
`oci://…` reference.

- Schema migrations run automatically on startup via `createSchema` in
  `internal/controlplane/migrations.go`. New tables and columns are added; no manual
  SQL required.
- **Back up before every upgrade.** Schema changes are additive but irreversible
  without a restore.
- **No rolling upgrade with SQLite.** SQLite allows only one writer; running two
  control plane replicas simultaneously will corrupt the database. The chart uses
  the `Recreate` strategy, so the old pod stops before the new one starts.
  Expect a brief downtime (seconds) during pod replacement.

### Upgrading agents

Upgrade the control plane first, then the agents, one cluster at a time. In
each target cluster:

```bash
helm get values kbridge-agent -n kbridge -o yaml > agent-values.yaml
helm upgrade kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.1-alpha.1 -f agent-values.yaml
rm agent-values.yaml
kubectl -n kbridge rollout status deploy/kbridge-agent
kb clusters list                     # the cluster's STATUS is connected again
```

The first command saves the values you set at install, so the upgrade keeps
them and takes the new chart's defaults, including its image tag. The file
holds the agent token if you set `controlPlane.token`, so the commands delete
it. From a clone, use `./charts/agent` in place of the `oci://…` reference.

The control plane works with agents one release older, so each cluster keeps
working while its agent waits; upgrade every agent before the next control
plane upgrade. The upgrade
rolls the agent pod the same way a [token rotation](#agent-token-rotation)
does: the new pod takes the cluster over as soon as it registers, and a stream
the old pod serves ends with it.

---

## Certificate renewal

```bash
kubectl -n kbridge get certificate kbridge-control-plane-tls -o jsonpath='{.status.renewalTime}'
kubectl -n kbridge rollout restart deploy/kbridge-control-plane
```

The control plane reads its TLS certificate only when it starts. When
cert-manager renews the Secret, restart the control plane before the old
certificate expires, or users and agents get `x509: certificate has expired`.
`renewalTime` is when cert-manager replaces the Secret: restart after that
time and before the old certificate's `notAfter`
(`-o jsonpath='{.status.notAfter}'`). After you add a host to the
Certificate's `dnsNames`, restart the same way once cert-manager reissues it.

## Agent-Token Rotation

Rotate in three steps: issue the new token, give it to the agent, then
revoke the old one. In that order the cluster stays connected.

**1. Issue the new token:**

```bash
kb admin agent-tokens create --cluster <cluster-name>
# Note the kbat_... value: it is shown only once.
```

**2. Give it to the agent**, the same way the agent was installed.

Inline token (`controlPlane.token`): the upgrade changes the config Secret,
which restarts the agent.

```bash
helm upgrade kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.1-alpha.1 \
  --reuse-values --set controlPlane.token=<new-token>
```

Existing Secret (`controlPlane.existingSecret`, `kbridge-agent-secrets` in the
[production guide](production-install.md)): update it, then restart the agent.

```bash
kubectl -n kbridge create secret generic kbridge-agent-secrets \
  --from-literal=token=<new-token> \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kbridge rollout restart deploy/kbridge-agent
```

**3. Check the agent reconnected, then revoke the old token:**

```bash
kubectl -n kbridge rollout status deploy/kbridge-agent
kb clusters list                                     # STATUS back to connected
kb admin agent-tokens list --cluster <cluster-name>  # the old token's ID
kb admin agent-tokens revoke <old-token-id>
```

To tell the tokens apart, match the `PREFIX` column against the first 13
characters of the old `kbat_...` value (still in the agent's previous Secret
or values), or pick the token whose last use stopped: `last_used_at` in
`GET /api/v1/admin/agent-tokens` and Last Used on the console's Agent Tokens
screen. The new token's last use is the agent's registration just now.

Revoke only after the agent is back on the new token: revocation disconnects
any agent still using the old one at once, and that agent stops
(CrashLoopBackOff) until it gets a new token.

If the control plane's `auth.token_pepper` (or `jwt_secret`) is rotated, all existing
agent tokens become invalid and must be re-issued.

### What happens to the agent during rotation

Hot rotation (no reconnect) is not implemented; each rotation rolls the agent
pod. That costs no outage: the new pod serves the cluster as soon as it
registers, and the old one, once replaced, stands by until Kubernetes stops it
(it does not exit or register again while the new agent is connected, so it
does not take the cluster back). If the new agent dies or disconnects first, the
standing-by agent registers again within 30 s and serves the cluster. A
command running on the old pod finishes there, within the pod's termination
grace period (`terminationGracePeriodSeconds`, 30 s by default); a longer one
is killed with the pod and fails with `agent shut down while the command was
running; retry it`. A stream the old pod serves (`logs -f`, `exec -it`,
`port-forward`) ends when it stops. A replaced agent that is never stopped
exits after 10 minutes with `replaced by a newer agent and never stopped:
exiting`; Kubernetes restarts it and it takes the cluster over, so two
replicas for one cluster swap it about every 10 minutes. Run one agent replica
per cluster (`replicaCount: 1`, the chart default).

---

## RBAC sync

```bash
kb admin rbac status
# CLUSTER   VERSION   STATE    ERRORS  REPORTED
# prod      426e2298  in sync  0       2026-11-05T20:09:44Z
# staging   1c9b07aa  drift    1       2026-11-05T20:09:41Z
#     binding kbridge-developer-admin: clusterRole admin is outside this cluster's accessCeiling (agent chart value accessCeiling.clusterRoles); ask the cluster owner to raise it
```

Each agent keeps its cluster's RoleBindings for `kbridge:<email>` users in
step with the policy's `clusterAccess` (see
[rbac.md](rbac.md#cluster-bindings-clusteraccess)). It reconciles when it
starts, when a heartbeat (every 30s) brings a new policy version, about 2s
after any `kbridge`-labelled binding changes in the cluster, and every
`sync.interval` (default `60s`, env `KBRIDGE_SYNC_INTERVAL`). A policy edit or
a new, disabled or deleted user therefore reaches each cluster within about
30s.

| State | Meaning |
|-------|---------|
| `in sync` | The agent's last report is for the current policy and every binding applied |
| `drift` | The last report is for an older policy version (wait one heartbeat), or some bindings could not be applied; the errors say which |
| `unreported` | The agent has not reported yet, or is older than this feature |

Someone editing a managed binding by hand is not drift for long: the agent
puts it back within a few seconds. Common errors:

- `clusterRole <name> is outside this cluster's accessCeiling`: policy binds a
  ClusterRole the agent chart's `accessCeiling.clusterRoles` does not list.
  The cluster owner adds it with `helm upgrade`, or you change the policy.
- any other `403` on create or update: the binding is in a namespace outside
  `accessCeiling.namespaces`, or is cluster-wide while
  `accessCeiling.clusterWide` is false. Raise that value with `helm upgrade`,
  or change the policy.
- `namespace "<ns>" is reserved for the kbridge agent`: policy names the
  agent's own namespace or a `protectedNamespaces` entry. Remove it from the
  policy; the agent never writes there.
- `exists and is not managed by kbridge`: a binding with the same name was
  made by hand. Delete it and the agent creates its own.
- `name is not a valid kubernetes name`: the role or ClusterRole name cannot
  form a binding name (capitals, `_`, `:`). Rename the role.

The ceiling is the agent chart's `accessCeiling`; see
[security.md](security.md#binding-kbridge-users-in-the-cluster) for the
permissions it renders.

---

## Troubleshooting Runbook

Find the row that starts with what you see.

| What you see | Cause | Fix |
|--------------|-------|-----|
| `Error: cluster "<name>": agent is disconnected` (a stream says `Error: cluster "<name>" cannot stream right now`, `exec -i` says `Error: cluster agent is disconnected`); `kb clusters list` shows `disconnected` | The cluster's agent is down, crash-looping, or cannot reach the control plane | `kubectl -n kbridge get pod -l app.kubernetes.io/name=kbridge-agent`, then `kubectl -n kbridge logs deploy/kbridge-agent --tail=50`: the log names the cause (next rows). A pod that restarts without an error failed its exec probe, which reads `/tmp/kbridge-agent-healthy`; see `kubectl -n kbridge describe pod <agent-pod>` |
| Agent log `Agent error: registering with control plane: registration rejected: invalid agent token`; the pod is in `CrashLoopBackOff` | The token is revoked, expired or wrong, or the control plane's `auth.token_pepper` (or, with the pepper empty, `jwt_secret`) changed | `kb admin agent-tokens list`: the agent's token `PREFIX` must be listed with `REVOKED` `false`; a token created with `--expires-in-days` also has an `expires_at` (`GET /api/v1/admin/agent-tokens`) that must be in the future. Issue a new one and give it to the agent as in [Agent-Token Rotation](#agent-token-rotation). Rotating `jwt_secret` with `token_pepper` empty does this to every agent (users are refreshed silently while their refresh token is valid): set a dedicated `token_pepper` before rotating, or re-issue every agent token afterwards |
| Agent log `dialing control plane: context deadline exceeded` | Port 9090 is not reachable from the agent, a NetworkPolicy blocks it, or the agent's TLS does not match the control plane's certificate | `kubectl -n kbridge exec deploy/kbridge-agent -- nc -zv <control-plane-host> 9090`; check that `controlPlane.tls.caCert` is the CA that signed the control plane's certificate and that the host is in its names ([production install §8](production-install.md#8-verify-the-deployment)) |
| `Error: authentication required: run 'kb login' first` | You are logged out, or the refresh token expired (`kb` refreshes the access token on its own until then) | `kb login` |
| `x509: certificate signed by unknown authority` | The control plane's certificate comes from a private CA the CLI does not trust | Give the CLI the CA as [security.md](security.md#4-trust-the-control-planes-ca-on-the-cli-keep-insecure_skip_verify-off) describes |
| `x509: certificate is valid for <names>, not <host>` | The host you dial is not in the certificate | Add it to the Certificate's `dnsNames` ([production install §3a](production-install.md#3a-issue-the-control-planes-certificate)) and restart once it is reissued ([Certificate renewal](#certificate-renewal)); for a dev certificate, regenerate it with `make certs` |
| `x509: certificate has expired` while the Secret is fresh | The control plane still serves the certificate it read at start | Restart the control plane ([Certificate renewal](#certificate-renewal)) |
| `Error: too many concurrent streams; try again later` (`logs -f`, `-w`) or `Error: too many concurrent sessions` (`exec -i`, `port-forward`) | `streams.max_concurrent` live sessions are open (default 50) | Raise `streams.max_concurrent` (`streams.maxConcurrent` in the chart), or wait for sessions to end |
| `Error: login failed: {"error":"too many attempts, slow down"}` | The [login rate limit](security.md#login-rate-limiting) | Wait a minute, then log in again |

---

## Logs & Health

### Control Plane

The control plane logs to stdout in a structured access-log format. In Kubernetes:

```bash
kubectl -n kbridge logs deploy/kbridge-control-plane -f
```

Each request line includes timestamp, method, path, status, latency, and client
IP. Health endpoint:

```bash
curl https://<control-plane-host>/health      # production install; add --cacert ca.crt for a private CA
curl http://localhost:8080/health             # quick start, through the port-forward
# {"status":"healthy","version":"v0.3.1-alpha.1"}
```

### Agent

The agent touches a sentinel file at `/tmp/kbridge-agent-healthy` on every
successful heartbeat. The chart's `exec` probes check its age: the pod is not
ready after 60 s without a heartbeat and is restarted after 180 s:

```yaml
livenessProbe:
  exec:
    command: ["sh", "-c", "test $(( $(date +%s) - $(stat -c %Y /tmp/kbridge-agent-healthy) )) -lt 180"]
```

If the agent stops sending heartbeats (e.g., the gRPC stream to the control plane drops),
the file goes stale and Kubernetes will restart the pod.

The control plane notices a dead agent within seconds, without waiting for
heartbeats: when the agent's gRPC stream closes, `kb clusters list` shows the
cluster `disconnected` and its queued commands fail with `agent is
disconnected`. If the agent's process died the stream closes at once; if its
node vanished, keepalive pings (every 5 s, 5 s to answer) close it within
about 10 s. An agent that is still alive reconnects and its next heartbeat
shows it `connected` again.

The agent runs one-shot commands (`get`, `apply`, `exec` without `-it`, ...)
one at a time, so a long one delays the others on the same cluster, for every
user, until it ends. Interactive exec, `logs -f`, `get -w` and `port-forward`
run on the agent's stream and are not delayed.

View agent logs:

```bash
kubectl -n kbridge logs deploy/kbridge-agent -f
```

---

## Known Limitations

| Area | Limitation |
|------|------------|
| **High availability** | SQLite is single-replica only — no HA or multi-writer support. Running more than one control plane replica will corrupt the database. PostgreSQL is not yet supported (SQLite only). |
| **Throughput** | `SetMaxOpenConns(1)` serializes all database access. Under heavy concurrent load, commands queue behind DB writes. This is a deliberate trade-off for SQLite correctness; a future PostgreSQL driver would remove it. |
| **Observability** | No Prometheus metrics endpoint. Operational visibility is limited to structured stdout logs and the audit log. |
| **Mutual TLS** | Only server-authenticated TLS is supported (control plane presents a certificate; clients verify it). Client certificates (mTLS) are not yet implemented. |
| **Port-forward idle timeout** | `kb port-forward` sessions have no idle timeout. A session with no traffic will hold the tunnel open indefinitely until Ctrl-C or pod restart. A proxy in front of the control plane may still close it; see [production install](production-install.md#why-not-an-http-ingress). |

## Next

- [Onboard a user](admin.md#onboard-a-user): accounts, roles and first passwords.
- [CLI reference](cli.md): what users run.
- [README](../README.md): back to the start.
