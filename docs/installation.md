# Installation

kbridge has three components: the **control plane** service, the cluster **agent**,
and the **CLI**. The control plane and agent are long-running; the CLI runs
on user machines.

## CLI installation

### One-liner installer (Linux / macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh | sh
```

Or with `wget`:

```bash
wget -qO- https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh | sh
```

The installer auto-detects the newest stable release, and falls back to the
newest pre-release (with a warning) when no stable release exists — which is the
case while kbridge is in alpha. To pin a version:

```bash
KBRIDGE_VERSION=v0.2.2-alpha.1 curl -fsSL \
  https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh | sh
```

To install into a custom directory (default is `/usr/local/bin`):

```bash
KBRIDGE_INSTALL_DIR=~/.local/bin curl -fsSL \
  https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh | sh
```

The installer downloads `kb_<version>_<os>_<arch>.tar.gz` and a `checksums.txt`
file from the GitHub release, verifies the SHA-256 checksum before extracting,
and places the `kb` binary in the install directory.

### Manual download

Download the binary for your platform directly from the
[GitHub Releases page](https://github.com/why-xn/kbridge/releases), verify the
checksum from `checksums.txt`, and place `kb` on your `$PATH`.

### Go install (fallback)

```bash
go install github.com/why-xn/kbridge/cmd/kb@latest
```

This requires Go 1.25+ and places `kb` in `$GOPATH/bin`.

## From source (binaries)

Requires Go 1.25+.

```bash
make build      # builds bin/kb (+ kbridge symlink), bin/kbridge-control-plane, bin/kbridge-agent
```

Run the control plane and an agent with example configs:

```bash
./bin/kbridge-control-plane --config configs/control-plane.yaml
./bin/kbridge-agent   --config configs/agent.yaml
```

## Docker

Multi-stage, CGO-free images (the agent image bundles `kubectl`):

```bash
make docker                                  # builds all three images
make docker IMAGE_PREFIX=ghcr.io/acme VERSION=v0.2.2-alpha.1
```

Images produced (with defaults): `kbridge-control-plane:latest`,
`kbridge-agent:latest`, `kbridge:latest`.

Run the control plane:

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v "$PWD/configs/control-plane.yaml:/etc/kbridge/control-plane.yaml:ro" \
  kbridge-control-plane:latest
```

## Helm (Kubernetes)

Charts are published as OCI artifacts to `oci://ghcr.io/why-xn/charts` on every
release, so no `helm repo add` is needed; Helm 3.8+ installs them straight from
the URL. Pin `--version` to the release you want (the chart version is the
release tag without the `v`).

The agent authenticates to the control plane with an **agent token**. There is
one per cluster, and the agent will not register without it, so the order is:
install the control plane, create the token, then install the agent with it.

### Step 1: Install the control plane

```bash
# Change the secrets for any real deployment
helm install kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  --version 0.2.2-alpha.1 \
  --set auth.jwtSecret="$(openssl rand -hex 32)" \
  --set auth.adminPassword="$(openssl rand -hex 12)"
```

### Step 2: Create the agent token

Log in as the seeded admin (`auth.adminEmail`, default `admin@kbridge.local`,
with the `auth.adminPassword` you set), then create one token per cluster:

```bash
kb login                      # prompts for the control plane URL, email and password
kb admin agent-tokens create --cluster prod-us-east
# Agent token: kba_xxxxxxxxxxxxxxxxxxxx   <- shown once; copy it now
```

The secret is shown only at creation. `kb admin agent-tokens list` shows
prefixes, never the secret; if it is lost, revoke it and create another. The
same token can also be created through `POST /api/v1/admin/agent-tokens` or
seeded through the control plane's `bootstrap` config (see the
[admin guide](admin.md)).

### Step 3: Install the agent in each target cluster

```bash
kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted
helm install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent -n kbridge \
  --version 0.2.2-alpha.1 \
  --set controlPlane.url=kbridge-control-plane-control-plane.default.svc:9090 \
  --set controlPlane.token=kba_xxxxxxxxxxxxxxxxxxxx \
  --set cluster.name=prod-us-east \
  --set 'protectedNamespaces={default}'   # the control plane's namespace, when co-located
```

`cluster.name` must match the `--cluster` the token was created for. For a
production install, keep the token in a Kubernetes Secret and point the chart at
it with `controlPlane.existingSecret` instead of `--set controlPlane.token`; the
[production guide](production-install.md) shows that flow.

From a clone of this repository, the same commands work with the local charts:
replace the `oci://…` reference with `./charts/control-plane` or `./charts/agent`
and drop `--version`.

Key chart values are documented in each chart's `values.yaml`. The agent chart
creates a ServiceAccount + ClusterRole that grants impersonation and binding
management; every command runs as `kbridge:<email>`, and the agent binds those
users as the policy's `clusterAccess` says, up to `accessCeiling` (see
[security.md](security.md#binding-kbridge-users-in-the-cluster)).

The agent never hands its ServiceAccount token to kubectl. The chart turns off
the default token mount and projects the token, CA and namespace read-only
at `/var/run/kbridge/sa` (`defaultMode: 0400`; the kubelet adds group read for
the pod's `fsGroup: 10001`, the agent user). Agent chart values for this:

| Value | Default | Purpose |
|---|---|---|
| `protectedNamespaces` | `[]` | Namespaces users can never touch through kbridge. The release namespace is always added; add the control plane's namespace when it runs in the same cluster. |
| `accessCeiling.clusterRoles` | `[view, edit, cluster-admin]` | ClusterRoles the agent may bind for kbridge users. Must not be empty. |
| `accessCeiling.namespaces` | `[]` | Namespaces the agent may write RoleBindings in; empty means any. Must not include the release namespace. |
| `accessCeiling.clusterWide` | `true` | Allow ClusterRoleBindings (`clusterWide: true` in policy). |
| `serviceAccountToken.expirationSeconds` | `600` | Lifetime of the projected token; the kubelet rotates it. |
| `networkPolicy.enabled` | `false` | Render an egress NetworkPolicy (recommended). Requires `apiServerCIDRs` and `controlPlaneTo`; rendering fails without them. |
| `networkPolicy.apiServerCIDRs` | `[]` | API server endpoint IPs, e.g. `kubectl get endpoints kubernetes -o jsonpath='{.subsets[*].addresses[*].ip}'` as `/32`s. |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | API server ports. |
| `networkPolicy.controlPlaneTo` | `[]` | NetworkPolicyPeer list for the control plane, e.g. `[{ipBlock: {cidr: 203.0.113.10/32}}]`. |
| `networkPolicy.controlPlanePort` | `9090` | Control plane gRPC port. |

With the policy on, the agent can reach only the API server, the control plane
and `kube-dns` in `kube-system` (53/UDP and TCP).

## Verifying

```bash
curl http://localhost:8080/health      # {"status":"healthy"}
kb login                          # authenticate
kb clusters list                  # list registered clusters
```

For production deployments, see [Production Install](production-install.md). Before
exposing the control plane to users, work through the
[Security Hardening Guide](security.md). For backup, upgrade, and troubleshooting
procedures, see the [Operations Guide](operations.md).
