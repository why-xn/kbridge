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
curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh \
  | KBRIDGE_VERSION=v0.3.1-alpha.1 sh
```

The default directory is `/usr/local/bin`, which usually needs `sudo`; the
installer asks for it when the directory is not writable. To install somewhere
on your own `PATH` instead:

```bash
curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh \
  | KBRIDGE_INSTALL_DIR="$HOME/.local/bin" sh
```

The installer downloads `kb_<version>_<os>_<arch>.tar.gz` and a `checksums.txt`
file from the GitHub release, verifies the SHA-256 checksum before extracting,
and places the `kb` binary in the install directory.

### Manual download

The release assets are archives, `kb_<version>_<os>_<arch>.tar.gz` for Linux
and macOS and `.zip` for Windows, plus `checksums.txt`. Download the one for
your platform and the checksums file from the
[GitHub Releases page](https://github.com/why-xn/kbridge/releases), then:

```bash
sha256sum -c --ignore-missing checksums.txt   # macOS: shasum -a 256 -c --ignore-missing checksums.txt
tar -xzf kb_<version>_<os>_<arch>.tar.gz kb
install -m 0755 kb ~/.local/bin/kb            # or anywhere on your PATH
```

### Go install (fallback)

```bash
go install github.com/why-xn/kbridge/cmd/kb@latest
```

This requires Go 1.25+ and places `kb` in `$GOPATH/bin`. It builds the newest
tagged release, but without version stamping: `kb --version` prints `dev`.
Prefer the installer when you need to report a version.

## From source (binaries)

Requires Go 1.25+.

```bash
make build      # builds bin/kb (+ kbridge symlink), bin/kbridge-control-plane, bin/kbridge-agent
```

Run the control plane and an agent with the example configs. The control
plane needs a JWT secret of at least 32 characters, and the agent needs a
kubeconfig when it runs outside a pod:

```bash
export KBRIDGE_JWT_SECRET="$(openssl rand -hex 32)"
./bin/kbridge-control-plane --config configs/control-plane.yaml

# In configs/agent.yaml, uncomment the kubernetes: block and set kubeconfig:
# to the absolute path of your kubeconfig, then:
./bin/kbridge-agent --config configs/agent.yaml

./bin/kb login   # http://localhost:8080, admin@kbridge.local / admin123
```

## Docker

Multi-stage, CGO-free images (the agent image bundles `kubectl`):

```bash
make docker                                  # builds all three images
make docker IMAGE_PREFIX=ghcr.io/acme VERSION=v0.3.1-alpha.1
```

Images are tagged with `git describe` (`dev` outside a git checkout), never
`latest`; set `VERSION` to choose. `make docker VERSION=local` produces
`kbridge-control-plane:local`, `kbridge-agent:local` and `kbridge:local`.

Run the published control plane image. The example config's paths do not
exist in the image, so point the database at the `kbridge` user's home on a
volume and mount the policy file next to the config:

```bash
sed -e 's#path: kbridge.db#path: /home/kbridge/kbridge.db#' \
    -e 's#policy_file: "configs/rbac.yaml"#policy_file: "/etc/kbridge/rbac.yaml"#' \
    configs/control-plane.yaml > control-plane.docker.yaml
docker volume create kbridge-data
docker run -p 8080:8080 -p 9090:9090 \
  -e KBRIDGE_JWT_SECRET="$(openssl rand -hex 32)" \
  -v kbridge-data:/home/kbridge \
  -v "$PWD/control-plane.docker.yaml:/etc/kbridge/control-plane.yaml:ro" \
  -v "$PWD/configs/rbac.yaml:/etc/kbridge/rbac.yaml:ro" \
  ghcr.io/why-xn/kbridge-control-plane:v0.3.1-alpha.1
curl http://localhost:8080/health   # {"status":"healthy"}
```

Use the tag from `make docker` in place of the published image to run a local
build.

## Helm (Kubernetes)

Charts are published as OCI artifacts to `oci://ghcr.io/why-xn/charts` on every
release, so no `helm repo add` is needed; Helm 3.8+ installs them straight from
the URL. Pin `--version` to the release you want (the chart version is the
release tag without the `v`).

This quick start installs the control plane and an agent into one cluster, in
the namespace `kbridge`. The agent authenticates to the control plane with an
**agent token**. There is one per cluster, and the agent will not register
without it, so the order is: install the control plane, create the token, then
install the agent with it.

### Step 1: Install the control plane

```bash
kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted

# Change the secrets for any real deployment; note the admin password
ADMIN_PASSWORD="$(openssl rand -hex 12)"; echo "admin password: $ADMIN_PASSWORD"
helm install kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.1-alpha.1 \
  --set auth.jwtSecret="$(openssl rand -hex 32)" \
  --set auth.adminPassword="$ADMIN_PASSWORD"
kubectl -n kbridge rollout status deploy/kbridge-control-plane
```

### Step 2: Create the agent token

Reach the control plane from your machine (leave this running in another
terminal):

```bash
kubectl -n kbridge port-forward svc/kbridge-control-plane 8080:8080
```

Log in as the seeded admin (`auth.adminEmail`, default `admin@kbridge.local`,
with the password from step 1), then create one token per cluster:

```bash
kb login
# Control plane URL: http://localhost:8080
# Email: admin@kbridge.local
# Password: <admin password from step 1>
kb admin agent-tokens create --cluster prod-us-east
# Agent token for cluster "prod-us-east" created.
#
#   kbat_xxxxxxxxxxxxxxxxxxxx
#
# Store it now — it cannot be retrieved again. ...
```

The secret is shown only at creation. `kb admin agent-tokens list` shows
prefixes, never the secret; if it is lost, revoke it and create another. The
same token can also be created through `POST /api/v1/admin/agent-tokens` or
seeded through the control plane's `bootstrap` config (see the
[admin guide](admin.md)).

### Step 3: Install the agent

```bash
helm install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.1-alpha.1 \
  --set controlPlane.url=kbridge-control-plane.kbridge.svc:9090 \
  --set controlPlane.token=kbat_xxxxxxxxxxxxxxxxxxxx \
  --set cluster.name=prod-us-east
```

The agent always protects its own namespace, so users cannot reach the control
plane's pods or Secrets in `kbridge` through kbridge. For an agent in another
cluster, create and label the `kbridge` namespace there first and set
`controlPlane.url` to an address that cluster can reach (see the
[production guide](production-install.md)).

### Step 4: Label namespaces where kbridge grants `edit`

The default policy grants only `view` and `cluster-admin`, so the quick start
needs no label. Do this when you add a role whose `clusterAccess` binds `edit`
(or any role that can create pods) in a namespace:

```bash
kubectl label ns <ns> pod-security.kubernetes.io/enforce=baseline
```

Without the label the agent refuses that binding and `kb admin rbac status`
shows the command to fix it.

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
| `sync.interval` | `60s` | How often the agent re-checks its bindings when nothing else triggers a sync, e.g. after a namespace is labelled for Pod Security. |
| `serviceAccountToken.expirationSeconds` | `600` | Lifetime of the projected token; the kubelet rotates it. |
| `networkPolicy.enabled` | `false` | Render an egress NetworkPolicy (recommended). Requires `apiServerCIDRs` and `controlPlaneTo`; rendering fails without them. |
| `networkPolicy.apiServerCIDRs` | `[]` | API server endpoint IPs, e.g. `kubectl get endpoints kubernetes -o jsonpath='{.subsets[*].addresses[*].ip}'` as `/32`s. |
| `networkPolicy.apiServerPorts` | `[443, 6443]` | API server ports. |
| `networkPolicy.controlPlaneTo` | `[]` | NetworkPolicyPeer list for the control plane, e.g. `[{ipBlock: {cidr: 203.0.113.10/32}}]`. |
| `networkPolicy.controlPlanePort` | `9090` | Control plane gRPC port. |

With the policy on, the agent can reach only the API server, the control plane
and `kube-dns` in `kube-system` (53/UDP and TCP).

## Verifying

With the port-forward from step 2 still running:

```bash
curl http://localhost:8080/health   # {"status":"healthy"}
kb clusters list                    # prod-us-east  connected
kb clusters use prod-us-east
kb get pods                         # runs as kbridge:admin@kbridge.local
```

For production deployments, see [Production Install](production-install.md). Before
exposing the control plane to users, work through the
[Security Hardening Guide](security.md). For backup, upgrade, and troubleshooting
procedures, see the [Operations Guide](operations.md).
