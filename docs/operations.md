# Operations Guide

Day-2 operations for kbridge: backup, upgrades, token rotation, troubleshooting,
and known limitations.

---

## Backup & Restore (SQLite)

The control plane stores all state in a single SQLite file at `/data/kbridge.db`
(the path set by `persistence.dbPath` in the Helm chart, defaulting to
`/data/kbridge.db`).

The image has no `sqlite3`, and a copy of a live WAL-mode database is not
consistent, so backup and restore stop the control plane for a moment and work
through a helper pod that mounts its PVC. The commands assume the release
`kbridge-control-plane` in namespace `kbridge`.

### Helper pod

```bash
kubectl -n kbridge scale deploy/kbridge-control-plane --replicas=0
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
# Start the helper pod (above), then:
kubectl -n kbridge exec kbridge-db -- rm -f /data/kbridge.db /data/kbridge.db-wal /data/kbridge.db-shm
kubectl -n kbridge exec -i kbridge-db -- tar -C /data -xf - < kbridge-backup.tar
kubectl -n kbridge delete pod kbridge-db
kubectl -n kbridge scale deploy/kbridge-control-plane --replicas=1
```

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

```bash
# Back up first (see above), then:
helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.0-alpha.1 \
  --reuse-values
```

The chart's `--version` and its default `image.tag` move together, so pin the
chart version rather than overriding the image tag. From a clone, use
`./charts/control-plane` in place of the `oci://…` reference.

- Schema migrations run automatically on startup via `createSchema` in
  `internal/controlplane/migrations.go`. New tables and columns are added; no manual
  SQL required.
- **Back up before every upgrade.** Schema changes are additive but irreversible
  without a restore.
- **No rolling upgrade with SQLite.** SQLite allows only one writer; running two
  control plane replicas simultaneously will corrupt the database. The chart uses
  the `Recreate` strategy, so the old pod stops before the new one starts.
  Expect a brief downtime (seconds) during pod replacement.

---

## Agent-Token Rotation

Hot rotation (no reconnect) is not implemented; each rotation costs one brief
agent reconnect. Issue the new token first:

```bash
kb admin agent-tokens create --cluster <cluster-name>
# Note the kbat_... value: it is shown only once.
```

Then give it to the agent, the same way the agent was installed.

Inline token (`controlPlane.token`): the upgrade changes the config Secret,
which restarts the agent.

```bash
helm upgrade kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.0-alpha.1 \
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

Then check the agent reconnected and revoke the old token:

```bash
kubectl -n kbridge rollout status deploy/kbridge-agent
kb clusters list                                     # STATUS back to connected
kb admin agent-tokens list --cluster <cluster-name>  # the old token's ID
kb admin agent-tokens revoke <old-token-id>
```

If the control plane's `auth.token_pepper` (or `jwt_secret`) is rotated, all existing
agent tokens become invalid and must be re-issued.

---

## RBAC sync

Each agent keeps its cluster's RoleBindings for `kbridge:<email>` users in
step with the policy's `clusterAccess` (see
[rbac.md](rbac.md#cluster-bindings-clusteraccess)). It reconciles when it
starts, when a heartbeat (every 30s) brings a new policy version, about 2s
after any `kbridge`-labelled binding changes in the cluster, and every
`sync.interval` (default `60s`, env `KBRIDGE_SYNC_INTERVAL`). A policy edit or
a new, disabled or deleted user therefore reaches each cluster within about
30s.

```bash
kb admin rbac status
# CLUSTER   VERSION   STATE    ERRORS  REPORTED
# prod      426e2298  in sync  0       2026-11-05T20:09:44Z
# staging   1c9b07aa  drift    1       2026-11-05T20:09:41Z
#     binding kbridge-developer-admin: POST /apis/rbac.authorization.k8s.io/v1/namespaces/team-a/rolebindings: 403 ...
```

| State | Meaning |
|-------|---------|
| `in sync` | The agent's last report is for the current policy and every binding applied |
| `drift` | The last report is for an older policy version (wait one heartbeat), or some bindings could not be applied; the errors say which |
| `unreported` | The agent has not reported yet, or is older than this feature |

Someone editing a managed binding by hand is not drift for long: the agent
puts it back within a few seconds. Common errors:

- `403` on create or update: the binding needs more than the agent chart's
  `accessCeiling` allows. Either raise the ceiling (`accessCeiling.clusterRoles`,
  `accessCeiling.namespaces`, `accessCeiling.clusterWide`) with
  `helm upgrade`, or change the policy.
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

| Symptom | Check | Resolution |
|---------|-------|------------|
| **Agent won't connect** | gRPC port 9090 reachable from agent pod? | `kubectl -n kbridge exec deploy/kbridge-agent -- nc -zv <control-plane-host> 9090` |
| | TLS CA mismatch? | Verify agent `control_plane.tls.ca_file` matches the cert served by the control plane |
| | Token invalid or revoked? | `kb admin agent-tokens list` — the agent's token `PREFIX` must be listed with `REVOKED` `false` (agent tokens do not expire); issue a new one if not |
| | `kb clusters list` shows "disconnected"? | Check agent logs: `kubectl -n kbridge logs deploy/kbridge-agent -f` |
| **401 from `kb`** | Access token expired? | `kb` auto-refreshes; if the refresh token is also expired, run `kb login` |
| | `jwt_secret` rotated on the control plane? | All sessions are invalidated — `kb login` required for all users |
| **TLS errors** | Certificate mismatch? | Verify the SAN on the cert covers the hostname the agent/CLI uses; regenerate with `make certs` for dev |
| | Self-signed cert rejected by CLI? | Set `insecure_skip_verify: true` in `~/.kbridge/config.yaml` (development only) |
| **503 from `kb`** | Agent pod down? | `kubectl -n kbridge get pod -l app.kubernetes.io/name=kbridge-agent` |
| | Agent disconnected? | `kubectl -n kbridge logs deploy/kbridge-agent --tail=50`; check for reconnect loop |
| | Agent heartbeat stale? | Exec probe reads `/tmp/kbridge-agent-healthy`; check `kubectl -n kbridge describe pod <agent-pod>` events |
| **429 Too Many Requests** | Concurrent session limit hit? | Default `streams.max_concurrent=50`; increase in `control-plane.yaml` or reduce concurrent users |

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
curl http://<control-plane-host>:8080/health
# {"status":"healthy"}
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
| **Port-forward idle timeout** | `kb port-forward` sessions have no idle timeout. A session with no traffic will hold the tunnel open indefinitely until Ctrl-C or pod restart. |
