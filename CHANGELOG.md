# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0-alpha.1] - 2026-11-06

### Added

- **The agent keeps cluster RBAC in step with kbridge policy.** A role's new
  `clusterAccess` list names the ClusterRole its users get, in which
  namespaces or cluster-wide. Each agent creates the `kbridge-<role>-<clusterRole>`
  bindings for the role's `kbridge:<email>` users, labelled
  `app.kubernetes.io/managed-by=kbridge`, puts them back within seconds when
  edited by hand, and deletes them when policy or the user goes away. It never
  writes in its own or a protected namespace and only binds `kbridge:` users.
  See [docs/rbac.md](docs/rbac.md#cluster-bindings-clusteraccess).
- **The agent grants a role that can create pods only where Pod Security is
  enforced.** A namespaced binding to any ClusterRole but `view` needs the
  namespace labelled `pod-security.kubernetes.io/enforce=baseline` (or
  `restricted`); otherwise `kb admin rbac status` shows the refusal and the
  label command, and a grant already made there is deleted. The agent chart grants `get` on namespaces for this, and a new
  `sync.interval` value (default `60s`) sets how often it re-checks. See
  [docs/rbac.md](docs/rbac.md#cluster-bindings-clusteraccess).
- **Agent chart `accessCeiling`** caps what the agent may bind: by default the
  ClusterRoles `view`, `edit` and `cluster-admin`, in any non-protected
  namespace and cluster-wide. Kubernetes refuses anything above it. The default
  policies map `admin` to `cluster-admin` and `viewer` to `view` cluster-wide,
  and the agent ceiling defaults to `[view, edit, cluster-admin]`.
- **`kb admin rbac status`** shows, per cluster, whether its bindings match the
  current policy (`in sync`, `drift`, `unreported`) and why not. See
  [docs/operations.md](docs/operations.md#rbac-sync).

**Upgrading:** add `clusterAccess` to your policy roles and set
`accessCeiling` to cover it (for example `clusterRoles: [view, edit, admin]`
or `clusterWide: true`), then upgrade the agents. Run `kb admin rbac status`
until every cluster is `in sync`. Then delete any `kbridge:` bindings you
made by hand (the agent ignores bindings without its label, so they keep
granting access until removed), e.g.
`kubectl get rolebindings,clusterrolebindings -A -o wide | grep kbridge:`.

### Security

- **`~/.kbridge/config.yaml` is private.** The CLI creates it 0600 (directory
  0700) and tightens an existing file on the next write; it holds the access
  and refresh tokens.
- **`default` applies only to users with no binding.** A bound user holds
  exactly the roles in their binding; before, every user also held the
  default role and the agent bound its `clusterAccess` to everyone. A policy
  that relied on that must list the default role in the binding
  (`roles: ["viewer", "developer"]`).
- **kubectl on the agent no longer holds the agent's credentials.** Before,
  the agent ran kubectl with its own ServiceAccount (cluster-admin by chart
  default) and passed user arguments through. A user allowed to run any
  command could add `--server`, `--token`, `--kubeconfig` or `--as` to send
  the agent's credentials elsewhere or act as someone else, reach the agent's
  own namespace however `-n` was spelled (`-nkbridge`), have the agent fetch
  `-f` URLs and read local files on the agent (`--from-file`, `-f <path>`), and
  use `kb cp` to read or write the agent's filesystem, including its token.
  Now kubectl talks to a loopback proxy in the agent with a one-off nonce; the
  proxy adds the agent token, drops `Authorization` and `Impersonate-*`
  headers, and refuses the agent's namespace, any `protectedNamespaces`, and
  the cluster-wide secrets list and watch. Connection flags, file flags and
  the verbs `cp`, `proxy`, `plugin`, `config` and `kustomize` are refused by
  the control plane (`403`, audited as `denied`) and again by the agent.
  Arguments after `--` are checked too, except a container command after
  `exec`, `attach`, `run` or `debug`; there a value flag just before `--` must
  be written `--flag=value`. See
  [docs/security.md](docs/security.md#layer-2--the-agent-what-kubectl-can-actually-do)
  for what this does and does not cover.
- **The agent chart mounts its token at `/var/run/kbridge/sa`** as a projected
  volume (10 minute expiry) and nothing at the default ServiceAccount path. A
  new `protectedNamespaces` value lists namespaces besides the release
  namespace that the agent refuses; add the control plane's namespace if it
  runs in the same cluster. An optional `networkPolicy` limits agent egress to
  the API server, the control plane and DNS.
- **The agent's proxy refuses the kubelet node proxy and ServiceAccount
  tokens.** `/api/v1/nodes/<node>/proxy/...` could run commands in any pod,
  the agent's included, and `kb create token <sa>` returned a token usable
  outside kbridge. Both are now refused with a `403` naming what was refused.
- **Pod Security grants follow the namespace label.** A pod-creating grant is
  deleted when its namespace stops enforcing `baseline` or `restricted`, and
  kept when the namespace cannot be read (only a definite answer revokes).
- **The policy finds the resource and namespace kubectl will use.** A flag
  value no longer stands in for the resource (`get -l pods secrets` was judged
  as `pods`), an unknown long flag before the resource takes the next token as
  its value, and `apply -f -` is checked against every namespace its manifests
  name, as is an attached `-nteam-b`.

**Upgrading:** upgrade the control plane and agents together (see
[Changed](#changed) below), then the CLI. Behaviour users will notice:

- `-f <URL>` is fetched by the `kb` CLI; an older CLI's `-f <URL>` is refused.
- `-k <dir>` is refused with a hint to run
  `kubectl kustomize <dir> | kb apply -f -`.
- `kb cp` is refused. Use `kb exec -i … -- tar` or similar instead.
- Commands into the agent's namespace or a protected namespace are refused,
  as is `kb get secrets -A`.

### Changed

- **Breaking: commands run as the requesting user, not as the agent.** The
  agent impersonates `kbridge:<email>` on every request to the API server, so
  Kubernetes RBAC bound to that user now decides what a command may do, after
  the kbridge policy allows it. Admission (PodSecurity and the like) applies as
  that user too. The agent chart's ClusterRole grants impersonation and the
  binding sync above; the `rbac.rules` value is removed. Each request carries the extra
  `kbridge-request-id`, visible in the Kubernetes audit log under
  `impersonatedUser`. See
  [docs/security.md](docs/security.md#binding-kbridge-users-in-the-cluster).

**Upgrading:** upgrade the control plane and all agents together. A new
agent refuses commands from an old control plane (no identity), and a new
control plane answers `503` for an old agent. Without a binding for its
`kbridge:<email>` user every request is `Forbidden`; the agent creates those
bindings from policy `clusterAccess` (see [Added](#added)).

- **Chart resources take standard Helm names.** Installed as `kbridge-agent`
  and `kbridge-control-plane`, the charts name their Deployment, Service,
  ServiceAccount, ClusterRole and Secrets `kbridge-agent` and
  `kbridge-control-plane` (before, the release name was followed by `-agent`
  or `-control-plane` again). A release name that does not contain
  the chart name gets `<release>-kbridge-agent` or
  `<release>-kbridge-control-plane`.

**Upgrading:** the control plane's data PVC is renamed too, so `helm upgrade`
deletes the old `<release>-control-plane-data` PVC and starts on an empty
database. Back up the database before upgrading and restore it afterwards (see
[docs/operations.md](docs/operations.md#backup--restore-sqlite)). Point
agents' `controlPlane.url` at the new Service name.

### Fixed

- **`kb edit` accepts an editor with arguments** (`KUBE_EDITOR="code --wait"`),
  split on spaces like kubectl does.
- **`kb admin rbac status` explains an `accessCeiling` refusal.** A binding
  to a ClusterRole outside the agent chart's `accessCeiling.clusterRoles`
  now reads `clusterRole <name> is outside this cluster's accessCeiling ...;
  ask the cluster owner to raise it` instead of the raw API server 403.
- **`-o go-template-file` and friends get their own refusal**, pointing at
  the inline `-o go-template='...'` form rather than the `--from-file` hint.
- **`install.sh` creates a custom `KBRIDGE_INSTALL_DIR`** instead of falling
  back to `sudo` when it does not exist yet.
- **Policy changes reach the running control plane.** A `helm upgrade` that
  changed `rbac.policy` left the old policy in force: the pod restart covered
  only the config Secret, and the kubelet's ConfigMap update did not fire the
  file watch. The chart now restarts the control plane when `rbac.policy`
  changes, and the control plane also re-reads the policy file every 10 s and
  reloads when its content changed. See [docs/rbac.md](docs/rbac.md#reloading).
- **The control plane chart stops the old pod before starting the new one**
  (`Recreate` strategy), so an upgrade never runs two SQLite writers or waits
  on a ReadWriteOnce volume still attached elsewhere, and sets `fsGroup: 10001`
  so the control plane can write its PVC on storage that mounts as root.
- **The install guides work as written.** The quick start installs both charts
  into one `kbridge` namespace labelled Pod Security `restricted`, reaches the
  control plane with `kubectl port-forward`, and labels the namespaces where
  kbridge grants `edit`. Backup and restore no longer need `sqlite3` in the
  image, token rotation covers both an inline token and `existingSecret`, and
  every command names the right namespace, Deployment and Secret. See
  [docs/installation.md](docs/installation.md#helm-kubernetes) and
  [docs/operations.md](docs/operations.md).
- **`kb login` prompts for the control plane URL** when none is set and
  reads a piped password (`echo "$PW" | kb login`).
- **Policy refusals say what was refused and who can help**, naming the verb,
  resource, namespace, cluster and user, and `kb` prints each error once
  without a usage block.
- **A dropped agent fails its commands.** Commands queued when an agent drops
  fail with `cluster "<name>": agent is disconnected` within the heartbeat
  timeout, with the same message and exit code (1) as a command sent
  afterwards; commands still running when the agent is replaced fail with
  `agent shut down while the command was running; retry it` instead of waiting
  for the command timeout. A reconnecting agent reports its sync state again
  so `kb admin rbac status` is current.
- **`kb -v=<n>` reaches kubectl**, viewers may run `kb auth can-i`, the
  `--from-file` refusal says how to send the object instead, and
  `kb admin rbac status` shows times in seconds.

## [0.2.2-alpha.1] - 2026-10-21

### Fixed

- **Restarting an agent no longer breaks its cluster.** A re-registered agent
  (rollout, crash, reschedule) now replaces the previous registration for its
  cluster. Before, the stale agent stayed registered and most requests failed
  with `agent is disconnected`. Commands queued for the replaced agent move to
  the new one, and an agent shutting down reports the command it was running
  instead of leaving the caller waiting for the command timeout.
- **In-cluster agents default to the `default` namespace.** Commands without
  `-n` ran in the agent's own namespace (e.g. `kbridge`). An explicit
  `POD_NAMESPACE` on the agent is still honoured.
- **`kb apply -f` (and `create`/`delete`/`replace`/… `-f`) works with local
  files.** Files, directories (`-R`) and `-f -` are read by the CLI and sent to
  the agent. Before, `-f` was mistaken for `--follow`: the path was resolved on
  the agent, failures exited 0, and `-f -` hung. Local `-k <dir>` now fails
  with a hint instead.
- **Streaming commands exit with kubectl's exit code.** `kb logs -f <missing>`
  exited 0.
- **`kb exec -i` forwards end of input.** Piping a script or file into
  `kb exec -i … -- sh` hung after the input ended.
- **First-run hint.** With no control plane URL configured, the CLI now names
  the config file and key to set instead of a non-existent `kb config set`.

## [0.2.1-alpha.1] - 2026-10-07

### Changed

- **Relicensed from Elastic License 2.0 to Apache License 2.0.** Releases up to
  and including `v0.2.0-alpha.1` remain under ELv2.

## [0.2.0-alpha.1] - 2026-08-24

### Changed

- **BREAKING — the "central" component is now the "control plane".** The rename
  is complete and there are no compatibility aliases: old names stop working.

  | Surface | Before | After |
  |---|---|---|
  | Binary | `kbridge-central` | `kbridge-control-plane` |
  | Image | `ghcr.io/why-xn/kbridge-central` | `ghcr.io/why-xn/kbridge-control-plane` |
  | Helm chart | `kbridge-central` | `kbridge-control-plane` |
  | In-cluster Service | `<release>-central` | `<release>-control-plane` |
  | Control plane config | `central.yaml` | `control-plane.yaml` |
  | Agent config key | `central:` | `control_plane:` |
  | Agent chart values | `central.*` | `controlPlane.*` |
  | CLI config key | `central_url` | `control_plane_url` |
  | Environment variable | `KBRIDGE_CENTRAL_URL` | `KBRIDGE_CONTROL_PLANE_URL` |
  | Go package | `internal/central` | `internal/controlplane` |
  | Proto message | `CentralStreamMessage` | `ControlPlaneStreamMessage` |

  The gRPC wire format is unchanged — only the message's name differs, and field
  numbers are untouched, so a renamed control plane and an older agent still
  interoperate at the protocol level.

  **Migration:**

  1. **CLI** — run `kb login` again, or rename `central_url` to
     `control_plane_url` in `~/.kbridge/config.yaml`. A stale `central_url` is
     ignored, and commands fail with "control plane URL not configured".
  2. **Helm** — the chart name and the Service name both change, so this is an
     uninstall/reinstall rather than an upgrade:
     `helm uninstall kbridge-central` then install
     `oci://ghcr.io/why-xn/charts/kbridge-control-plane`. Set
     `persistence.existingClaim` to the previous PVC to retain the database.
  3. **Agents** — point `controlPlane.url` at the new Service DNS
     (`kbridge-control-plane:9090`) and reinstall the agent chart. Agent tokens
     and the RBAC policy file are unaffected.

## [0.1.0-alpha.1] - 2026-08-24

First public pre-release. The feature set below is complete and end-to-end
tested, but interfaces — config keys, the RBAC policy schema, and REST API
paths — may still change before 1.0.0.

### Added

- **Multi-cluster kubectl proxy** — control plane + per-cluster agents connected via outbound gRPC; no inbound firewall rules or VPN required.
- **CLI (`kb`)** — kubectl-compatible command-line tool; anything that isn't a management command is forwarded to the selected cluster. `kbridge` remains as a back-compat symlink.
- **JWT authentication** — HS256 access tokens (1 h TTL) with opaque refresh tokens; the CLI transparently refreshes expired tokens so interactive sessions are uninterrupted.
- **Login rate limiting** — failed login attempts are rate-limited per IP to slow brute-force attacks; the server fails closed (startup aborted) if rate-limiter bounds cannot be satisfied.
- **Declarative file-based RBAC** — hot-reloaded policy file (ArgoCD-style) with roles, wildcarded rules (cluster / namespace / resource / verb), and user bindings by JWT email.
- **Audit logging** — every command (allowed, denied, failed, or timed out) recorded with user, cluster, command, result, and duration; queryable via `kb admin audit` and `GET /api/v1/admin/audit`.
- **Streaming logs and watch** — `kb logs -f` / `kb get pods -w` stream output live to the terminal until Ctrl-C.
- **Interactive exec** — `kb exec -it` allocates a full PTY for interactive shells; `-i` provides stdin-only mode.
- **Port-forward** — `kb port-forward` tunnels a pod port to localhost.
- **Server-authenticated TLS** — configurable for the HTTP REST API and the agent↔control plane gRPC channel; `make certs` generates a dev certificate.
- **Agent heartbeat health file + exec probe** — the agent writes a liveness file on every heartbeat; the Helm chart configures an exec-based liveness probe against it.
- **Secrets from file, environment variable, or inline** — `jwt_secret`, `token_pepper`, and agent tokens can be supplied as a file path (`_file` suffix), an env var, or a plain inline value; secrets shorter than 32 characters are rejected at startup.
- **Build-time version stamping** — `--version` flag on all three binaries; version, commit, and build date injected via `-ldflags` at `make build` time.
- **Helm charts** — production-grade charts for control plane and agent; support `existingSecret`, pod security contexts, control plane `token_pepper` and `streams` config, and the agent heartbeat exec probe.
- **End-to-end test suite** — Kind-based E2E tests covering login rate limiting, secret-from-file boot, and fail-closed startup (`make test-e2e`).

### Changed

- SQLite concurrency hardened: `busy_timeout` set and a single-writer serialisation lock applied to prevent `SQLITE_BUSY` errors under concurrent requests.
- Access token lifetime reduced to 1 hour (previously longer-lived); refresh rotation keeps sessions alive transparently.
- Helm chart: agent and control plane now read secrets from mounted files by default when `existingSecret` is configured, avoiding environment-variable exposure.
- Helm chart: pod security contexts tightened (non-root user, read-only root filesystem, dropped capabilities).
- Trusted-proxy configuration added to the Gin HTTP server to ensure accurate client-IP detection behind a load balancer or ingress.

### Security

- **Strong-secret enforcement (fail-closed)** — the control plane refuses to start if `jwt_secret` or `token_pepper` is shorter than 32 characters, preventing accidental weak-key deployments.
- **Agent token storage** — tokens are stored only as HMAC-SHA256 digests keyed by a server-side pepper; a stolen database alone cannot be used to verify guessed tokens.
- mTLS between agents and control plane is planned but deferred; the channel is currently protected by server-authenticated TLS only.

[Unreleased]: https://github.com/why-xn/kbridge/compare/v0.3.0-alpha.1...HEAD
[0.3.0-alpha.1]: https://github.com/why-xn/kbridge/compare/v0.2.2-alpha.1...v0.3.0-alpha.1
[0.2.2-alpha.1]: https://github.com/why-xn/kbridge/compare/v0.2.1-alpha.1...v0.2.2-alpha.1
[0.2.1-alpha.1]: https://github.com/why-xn/kbridge/compare/v0.2.0-alpha.1...v0.2.1-alpha.1
[0.2.0-alpha.1]: https://github.com/why-xn/kbridge/compare/v0.1.0-alpha.1...v0.2.0-alpha.1
[0.1.0-alpha.1]: https://github.com/why-xn/kbridge/releases/tag/v0.1.0-alpha.1
