# End-to-End Tests

This directory contains end-to-end tests for mk8s using Kind (Kubernetes in Docker).

## Prerequisites

- Docker installed and running
- [Kind](https://kind.sigs.k8s.io/) installed
- kubectl installed
- Go 1.21+

## Running Tests

### Full E2E Test Suite

Run the complete e2e test suite:

```bash
make test-e2e
```

This will:
1. Create a Kind cluster
2. Build all binaries
3. Start the central service
4. Start an agent connected to Kind
5. Run all e2e tests
6. Clean up everything

### Manual Testing Setup

For manual testing or debugging, you can set up the environment without running tests:

```bash
# Setup the environment (Kind cluster, central, agent)
make e2e-setup

# Now you can manually run CLI commands
./bin/mk8s clusters list
./bin/mk8s clusters use mk8s-e2e-test
./bin/mk8s kubectl get pods -A

# When done, tear down the environment
make e2e-teardown
```

### Running Individual Tests

With the environment set up via `make e2e-setup`, you can run individual tests:

```bash
go test -v -tags=e2e ./tests/e2e/... -run TestCLIClustersLis
```

## Test Coverage

The e2e tests cover:

### Central Service
- Health check endpoint
- Cluster listing API

### Agent
- Registration with central
- Heartbeat keeping connection alive

### CLI Commands
- `mk8s clusters list` - Lists all clusters
- `mk8s clusters ls` - Alias for list
- `mk8s clusters use <name>` - Selects a cluster
- `mk8s status` - Shows current status
- `mk8s kubectl <args>` - Executes kubectl commands
- `mk8s k <args>` - Alias for kubectl
- Help commands for all subcommands

### kubectl Operations
- `get pods -A` - List all pods across namespaces
- `get nodes` - List cluster nodes
- `get namespaces` - List namespaces
- Non-zero exit code handling

### kubectl edit Operations
The edit tests use mock editor scripts via the `KUBE_EDITOR` environment variable to simulate interactive editing:

- **TestKubectlEditConfigMap** - Edit a ConfigMap, verify value changes are persisted
- **TestKubectlEditDeployment** - Edit a Deployment, verify container image changes
- **TestKubectlEditService** - Edit a Service, verify port configuration changes
- **TestKubectlEditWithSlashFormat** - Test `edit configmap/name` syntax
- **TestKubectlEditCancel** - Exit editor without changes, verify no modifications
- **TestKubectlEditResourceNotFound** - Attempt to edit non-existent resource, verify error
- **TestKubectlEditInvalidYAML** - Introduce invalid YAML, verify rejection and data preservation
- **TestKubectlEditNamespaceFlags** - Test `--namespace=` flag format

## Logs

When running tests, logs are stored in `tests/e2e/logs/`:
- `central.log` - Central service logs
- `agent.log` - Agent logs

## Configuration

Test configuration files are stored in `tests/e2e/config/`:
- `central.yaml` - Central service config
- `agent.yaml` - Agent config

These directories are cleaned up automatically after tests.

## Troubleshooting

### Tests fail to start

1. Ensure Docker is running
2. Ensure Kind is installed: `kind version`
3. Check for port conflicts on 8080 (HTTP) and 9090 (gRPC)

### Cluster creation fails

```bash
# Check existing Kind clusters
kind get clusters

# Delete stale cluster manually
kind delete cluster --name mk8s-e2e-test
```

### View test logs

```bash
# View central logs
cat tests/e2e/logs/central.log

# View agent logs
cat tests/e2e/logs/agent.log
```
