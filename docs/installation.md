# Installation

kbridge has three components: the **central** service, the cluster **agent**,
and the **CLI**. The central service and agent are long-running; the CLI runs
on user machines.

## From source (binaries)

Requires Go 1.25+.

```bash
make build      # builds bin/kbridge, bin/kbridge-central, bin/kbridge-agent
```

Run central and an agent with example configs:

```bash
./bin/kbridge-central --config configs/central.yaml
./bin/kbridge-agent   --config configs/agent.yaml
```

## Docker

Multi-stage, CGO-free images (the agent image bundles `kubectl`):

```bash
make docker                                  # builds all three images
make docker IMAGE_PREFIX=ghcr.io/acme VERSION=v1.0.0
```

Images produced (with defaults): `kbridge-central:latest`,
`kbridge-agent:latest`, `kbridge:latest`.

Run central:

```bash
docker run -p 8080:8080 -p 9090:9090 \
  -v "$PWD/configs/central.yaml:/etc/kbridge/central.yaml:ro" \
  kbridge-central:latest
```

## Helm (Kubernetes)

Charts live in `charts/central` and `charts/agent`.

```bash
# Central — change the secrets for any real deployment
helm install kbridge-central ./charts/central \
  --set auth.jwtSecret="$(openssl rand -hex 32)" \
  --set auth.adminPassword="$(openssl rand -hex 12)"

# Agent — deployed into each target cluster
helm install kbridge-agent ./charts/agent \
  --set central.url=kbridge-central:9090 \
  --set central.token=<agent-token> \
  --set cluster.name=prod-us-east
```

Generate an agent token first with `kbridge admin agent-tokens` (see the
[admin guide](admin.md)) or seed one via central's `bootstrap` config.

Key chart values are documented in each chart's `values.yaml`. The agent chart
creates a ServiceAccount + ClusterRole; scope `rbac.rules` down to what your
users actually need (the kbridge policy file is the per-user gate).

## Verifying

```bash
curl http://localhost:8080/health      # {"status":"healthy"}
kbridge login                          # authenticate
kbridge clusters list                  # list registered clusters
```
