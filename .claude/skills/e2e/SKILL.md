---
name: e2e
description: Use when running, iterating on, or debugging kbridge end-to-end tests on Kind (make test-e2e, e2e-setup, tests/e2e), including when a port is busy, a single test must be rerun, or the environment was left half up.
---

# Running e2e correctly

`scripts/e2e-kind.sh` owns the whole environment: Kind cluster
`kbridge-e2e-test`, control plane and agent as host processes, a fixed dev
agent token, and a CLI config it writes over `~/.kbridge/config.yaml`. The Go
tests in `tests/e2e/` (build tag `e2e`) drive `bin/kb` against that.

## Quick reference

| Goal | Command |
|---|---|
| Whole suite, clean cluster, auto cleanup | `make test-e2e` |
| Ports 8080/9090 busy | prefix any command with `CONTROL_PLANE_PORT=18080 GRPC_PORT=19090` (`NOTIFY_PORT` for 18999) |
| Keep the environment up to iterate | `make e2e-setup` ... `make e2e-teardown` |
| One test against a running environment | `go test -v -count=1 -tags=e2e ./tests/e2e/ -run '^TestName$' -control-plane-url=http://localhost:$CONTROL_PLANE_PORT -grpc-addr=localhost:$GRPC_PORT` |
| Logs | `tests/e2e/logs/control-plane.log`, `tests/e2e/logs/agent.log` |
| Stale cluster | `kind delete cluster --name kbridge-e2e-test` |

The test flags default to 8080/9090, so pass them whenever the ports were
overridden. The old name `CENTRAL_PORT` does nothing.

## Iterating

1. `make e2e-setup` once.
2. Edit, then rebuild only what changed: `make build-cli` for CLI code. A
   control plane or agent change needs `make e2e-teardown` and `make e2e-setup`
   again, since the script starts them from `bin/`.
3. Run the one test with `-run`. Use `-count=1` so Go does not cache a pass.
4. Finish with a full `make test-e2e` on a fresh cluster before claiming done.

## Protect the developer's CLI config

`setup` and `test` back up `~/.kbridge/config.yaml` to `config.yaml.backup`,
one copy only, and restore it on teardown. Running `setup` twice overwrites the
backup with the e2e config and the real one is lost. Before touching e2e on a
machine with a real config:

```bash
cp ~/.kbridge/config.yaml ~/.kbridge/config.yaml.pre-e2e
```

If a run was killed (SIGKILL, closed terminal) the EXIT trap never ran: run
`make e2e-teardown`, then confirm `kb` points back at the real control plane.

## Also true

- `kind create` switches the current kubectl context to `kind-kbridge-e2e-test`
  and deletion leaves no current context. Note `kubectl config current-context`
  before, and `kubectl config use-context <it>` after teardown.
- `make e2e-teardown` stops processes by pid file; it does not need the port
  variables.
- The port check only notices a busy port that answers `/health`; anything
  else shows up as the control plane failing to start.
- `TestAgentAuth` starts its own unauthorized agent as a host process; it is
  the only test that bypasses the harness agent.
- CI runs the suite nightly (`.github/workflows/e2e.yml`), not on every PR.
  A PR that changes cross-component behaviour must run it locally.
