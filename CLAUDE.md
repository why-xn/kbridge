# kbridge Development Guide

kbridge is a safer kubectl: a CLI, a control plane, and an in-cluster agent.
Users hold no cluster credentials; every command is checked, run as the user,
and recorded. Think, plan, and execute as a Staff Software, DevOps, and Site
Reliability Engineer. `ROADMAP.md` says where the project is going.

## Project Structure

```
cmd/kb/                 # CLI entry point (binary `kb`, `kbridge` symlink)
cmd/controlplane/       # Control plane entry point
cmd/agent/              # Agent entry point
internal/cli/           # CLI commands (Cobra): what the user types and sees
internal/controlplane/  # Control plane: HTTP + gRPC servers, authz, store, audit, grants
internal/agent/         # Agent: gRPC client, kubectl executor, streams, port-forward
internal/policy/        # RBAC + guardrail decisions. Pure: no I/O, no server types
internal/auth/          # JWT, passwords, auth middleware
internal/execframe/     # Wire framing for interactive exec
internal/pfframe/       # Wire framing for port-forward
internal/version/       # Build-time version stamping
api/proto/              # Protobuf definitions
api/proto/agentpb/      # Generated gRPC code — do not edit by hand
charts/                 # Helm charts (control-plane, agent)
build/                  # Dockerfiles
configs/                # Example YAML configs
scripts/                # Build and test scripts
tests/e2e/              # End-to-end tests (require Kind + Docker)
```

## Build & Test Commands

- `make build` — build all three binaries to `bin/`
- `make test` — run unit tests with coverage
- `make test-e2e` — run E2E tests (requires Docker and Kind). Read the
  `e2e` skill in `.claude/skills/e2e/` before running it
- `make proto` — regenerate gRPC code from `api/proto/agent.proto`

## Principles

Security is the floor, not a principle to trade off. Nothing below overrides
it:

- A user never receives a cluster credential, through any path: not in output,
  not through a kubectl flag, not through a file the agent can read.
- The agent sets identity; the client only claims it. Anything a client sends
  is hostile until checked.
- Every check fails closed. Unknown verb, unknown flag, unknown agent
  capability: refuse, with a message that says what would be allowed.
- A new flag, path, or endpoint is assumed to widen access until shown
  otherwise. Say in the PR how it was shown.

Then, in priority order:

1. **User first.** Design every change as if you are the user, and ask whether
   you would want to use it. Concretely: an error says what to do next; one
   command does the job, not three; defaults work with no flags; the chart
   asks for the minimum; a refusal says why and who can unblock it; output
   reads at a glance.
2. **Simple.** The smallest change that fully solves the task, including the
   callers, tests, docs, and chart values it touches. Reuse what the codebase
   has before adding anything.
3. **Structured.** New code lands in the package that owns the domain (see
   below). Structure is kept, not reinvented.
4. **Creative when it is hard.** A bug fix gets the boring fix. A design
   problem (new security boundary, a feature that touches all three
   components, a limit of the current shape) gets alternatives first: look for
   the shape that removes the class of problem rather than patches one case,
   spike the risky part in a throwaway, and bring what the spike taught back
   before writing a plan.

## Where code goes

- User-facing wording, flags, prompts, and exit codes: `internal/cli/`.
- Who may do what: `internal/policy/` decides, `internal/controlplane/`
  enforces. The policy package stays free of HTTP, gRPC, and storage.
- Anything that touches the cluster: `internal/agent/`.
- A shared type goes in the package that owns the concept. There is no `utils`,
  `helpers`, or `common` package and none will be added.
- Unit tests sit next to the code. Behaviour that crosses two components gets
  an e2e test in `tests/e2e/`.
- Config follows `DefaultConfig() → LoadConfig(path) → applyEnvOverrides() →
  Validate()` in every component. New keys get a default, an env override, and
  a validation rule.

## What over-engineering looks like here

Refuse these unless the task asks for them by name:

- An option, flag, or config key for something with one sensible value.
- An interface with one implementation.
- A new package for one function.
- A background job where a check at request time would do.
- A compatibility layer for a version that was never released.
- "For later" code paths, TODO scaffolding, or feature flags.

## Code Style

- Domain-driven packages; keep functions under 20 lines.
- Prefer composition over inheritance.
- Table-driven unit tests.
- Error messages: lowercase, no trailing punctuation, wrapped with context
  (`fmt.Errorf("reading config: %w", err)`).
- Comment only the why the code cannot show.

## Tech Stack & Patterns

- **CLI**: Cobra + Viper (`internal/cli/`)
- **HTTP server**: Gin (`internal/controlplane/http.go`)
- **RPC**: gRPC + Protocol Buffers (`internal/controlplane/grpc.go`, `internal/agent/agent.go`)
- **Config**: YAML files with environment variable overrides
- **Store**: SQLite (`internal/controlplane/store.go`)

### Proto changes

Edit `api/proto/agent.proto`, then run `make proto`. Never edit files in
`api/proto/agentpb/` directly. Control plane and agent must tolerate the other
side being one release older: new fields are optional, new RPCs are gated by a
capability the agent reports.

## Working rules

- **Commits**: Conventional Commits (`feat`, `fix`, `docs`, `test`,
  `refactor`, `chore`, `build`, `ci`), imperative summary, body says why.
  No AI attribution lines (no `Co-Authored-By`, no session links).
- **Never commit** `docs/plans/` or `docs/superpowers/`. They are local and
  gitignored. Never commit `.claude/settings.local.json`.
- **Line endings**: files are mixed CRLF and LF. Keep each file's existing
  ending; never convert a file as a side effect of editing it.
- **Plans**: a multi-task change gets a plan in `docs/plans/`, is implemented
  task by task with subagents, and gets one whole-branch review at the end,
  not a review per task.
- **Branching for fixes that must ship alone**: branch from the latest release
  tag, not `master`, cut the release from that branch, then merge the branch
  into `master` with `--no-ff`. See the `release` skill.
- **Docs ship with the change**: `CHANGELOG.md` under `[Unreleased]`, and the
  page in `docs/` that describes the behaviour. `ROADMAP.md` moves an item
  only when it is released.

## Skills

Repo-local skills live in `.claude/skills/`. Read the skill before doing the
task it covers:

- `e2e` — running end-to-end tests on Kind correctly, iterating on one test.
- `release` — cutting and verifying a release.
