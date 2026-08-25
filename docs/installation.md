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
KBRIDGE_VERSION=v0.2.0-alpha.1 curl -fsSL \
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
make docker IMAGE_PREFIX=ghcr.io/acme VERSION=v0.2.0-alpha.1
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

Charts live in `charts/control-plane` and `charts/agent`.

```bash
# Control Plane — change the secrets for any real deployment
helm install kbridge-control-plane ./charts/control-plane \
  --set auth.jwtSecret="$(openssl rand -hex 32)" \
  --set auth.adminPassword="$(openssl rand -hex 12)"

# Agent — deployed into each target cluster
helm install kbridge-agent ./charts/agent \
  --set control_plane.url=kbridge-control-plane:9090 \
  --set control_plane.token=<agent-token> \
  --set cluster.name=prod-us-east
```

Generate an agent token first with
`kb admin agent-tokens create --cluster <name>` (see the
[admin guide](admin.md)) — or via the `POST /api/v1/admin/agent-tokens` API, or
by seeding one through the control plane's `bootstrap` config.

Key chart values are documented in each chart's `values.yaml`. The agent chart
creates a ServiceAccount + ClusterRole; scope `rbac.rules` down to what your
users actually need (the kbridge policy file is the per-user gate).

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
