# Operations Guide

Day-2 operations for kbridge: backup, upgrades, token rotation, troubleshooting,
and known limitations.

---

## Backup & Restore (SQLite)

The central service stores all state in a single SQLite file at `/data/kbridge.db`
(the path set by `persistence.dbPath` in the Helm chart, defaulting to
`/data/kbridge.db`).

### Online backup (no downtime)

SQLite's `.backup` command creates a consistent snapshot while the database is in
use — no shutdown required.

```bash
# 1. Create the backup inside the pod
kubectl exec deploy/kbridge-central -- \
  sqlite3 /data/kbridge.db ".backup '/data/backup.db'"

# 2. Copy it to your local machine
POD=$(kubectl get pod -l app.kubernetes.io/name=kbridge-central \
      -o jsonpath='{.items[0].metadata.name}')
kubectl cp "${POD}:/data/backup.db" ./kbridge-backup-$(date +%Y%m%d).db
```

### Restore

Restoring replaces the live file, so a brief outtime is required:

```bash
# 1. Scale central to zero to close all DB connections
kubectl scale deploy/kbridge-central --replicas=0

# 2. Copy the backup file into the PVC via a temporary pod or kubectl cp
POD=$(kubectl get pod -l app.kubernetes.io/name=kbridge-central \
      -o jsonpath='{.items[0].metadata.name}')
kubectl cp ./kbridge-backup.db "${POD}:/data/kbridge.db"

# 3. Restore replicas
kubectl scale deploy/kbridge-central --replicas=1
```

### PVC snapshots

If your storage class supports `VolumeSnapshot`, take a PVC snapshot for
point-in-time cluster-level recovery before destructive changes:

```bash
kubectl apply -f - <<EOF
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: kbridge-central-data-$(date +%Y%m%d)
spec:
  source:
    persistentVolumeClaimName: kbridge-central-data
EOF
```

---

## Upgrades

### Helm upgrade

```bash
# Back up first (see above), then:
helm upgrade kbridge-central ./charts/central \
  --reuse-values \
  --set image.tag=v1.1.0
```

- Schema migrations run automatically on startup via `createSchema` in
  `internal/central/migrations.go`. New tables and columns are added; no manual
  SQL required.
- **Back up before every upgrade.** Schema changes are additive but irreversible
  without a restore.
- **No rolling upgrade with SQLite.** SQLite allows only one writer; running two
  central replicas simultaneously will corrupt the database. Upgrade by replacing
  the single pod (the default Helm `RollingUpdate` with `maxUnavailable=1` and
  `maxSurge=0` achieves this; the default chart uses a `Recreate` equivalent).
  Expect a brief downtime (seconds) during pod replacement.

---

## Agent-Token Rotation

Hot rotation (no reconnect) is not implemented; follow these steps for a
zero-disruption rotation with one brief agent reconnect:

```bash
# 1. Issue a new token for the cluster
kb admin agent-tokens create --cluster <cluster-name>
# Note the token value — it is shown only once.

# 2. Update the agent Secret with the new token
kubectl -n <agent-namespace> create secret generic kbridge-agent \
  --from-literal=token=<new-token> \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. Restart the agent so it picks up the new Secret
kubectl -n <agent-namespace> rollout restart deploy/kbridge-agent

# 4. Verify the agent reconnects
kubectl -n <agent-namespace> rollout status deploy/kbridge-agent
kb clusters list   # status should return to "connected"

# 5. Revoke the old token
kb admin agent-tokens revoke <old-token-id>
```

If central's `auth.token_pepper` (or `jwt_secret`) is rotated, all existing
agent tokens become invalid and must be re-issued.

---

## Troubleshooting Runbook

| Symptom | Check | Resolution |
|---------|-------|------------|
| **Agent won't connect** | gRPC port 9090 reachable from agent pod? | `kubectl exec -n <ns> deploy/kbridge-agent -- nc -zv <central-host> 9090` |
| | TLS CA mismatch? | Verify agent `central.tls.ca_file` matches the cert served by central |
| | Token invalid or expired? | `kb admin agent-tokens list` — check `is_revoked` and `expires_at`; re-issue if needed |
| | `kb clusters list` shows "disconnected"? | Check agent logs: `kubectl logs deploy/kbridge-agent -f` |
| **401 from `kb`** | Access token expired? | `kb` auto-refreshes; if the refresh token is also expired, run `kb login` |
| | `jwt_secret` rotated on central? | All sessions are invalidated — `kb login` required for all users |
| **TLS errors** | Certificate mismatch? | Verify the SAN on the cert covers the hostname the agent/CLI uses; regenerate with `make certs` for dev |
| | Self-signed cert rejected by CLI? | Set `insecure_skip_verify: true` in `~/.kbridge/config.yaml` (development only) |
| **503 from `kb`** | Agent pod down? | `kubectl get pod -l app.kubernetes.io/name=kbridge-agent` |
| | Agent disconnected? | `kubectl logs deploy/kbridge-agent --tail=50`; check for reconnect loop |
| | Agent heartbeat stale? | Exec probe reads `/tmp/kbridge-agent-healthy`; check `kubectl describe pod <agent-pod>` events |
| **429 Too Many Requests** | Concurrent session limit hit? | Default `streams.max_concurrent=50`; increase in `central.yaml` or reduce concurrent users |

---

## Logs & Health

### Central

Central logs to stdout in a structured access-log format. In Kubernetes:

```bash
kubectl logs deploy/kbridge-central -f
```

Each request line includes timestamp, method, path, status, latency, and client
IP. Health endpoint:

```bash
curl http://<central-host>:8080/health
# {"status":"healthy"}
```

### Agent

The agent writes a sentinel file at `/tmp/kbridge-agent-healthy` on every
successful heartbeat. The Kubernetes Deployment uses an `exec` liveness probe
that checks for this file:

```yaml
livenessProbe:
  exec:
    command: ["/bin/sh", "-c", "test -f /tmp/kbridge-agent-healthy"]
```

If the agent stops sending heartbeats (e.g., the gRPC stream to central drops),
the file goes stale and Kubernetes will restart the pod.

View agent logs:

```bash
kubectl logs deploy/kbridge-agent -f
```

---

## Known Limitations

| Area | Limitation |
|------|------------|
| **High availability** | SQLite is single-replica only — no HA or multi-writer support. Running more than one central replica will corrupt the database. PostgreSQL is not yet supported (SQLite only). |
| **Throughput** | `SetMaxOpenConns(1)` serializes all database access. Under heavy concurrent load, commands queue behind DB writes. This is a deliberate trade-off for SQLite correctness; a future PostgreSQL driver would remove it. |
| **Observability** | No Prometheus metrics endpoint. Operational visibility is limited to structured stdout logs and the audit log. |
| **Mutual TLS** | Only server-authenticated TLS is supported (central presents a certificate; clients verify it). Client certificates (mTLS) are not yet implemented. |
| **Port-forward idle timeout** | `kb port-forward` sessions have no idle timeout. A session with no traffic will hold the tunnel open indefinitely until Ctrl-C or pod restart. |
