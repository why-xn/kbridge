# kbridge Roadmap

_Last updated 2026-09-11. This is the one place for what is done, what is being
built, and what comes next. History of how each shipped feature was built lives
in the git log and `CHANGELOG.md`._

## The idea in one paragraph

kbridge is a **front door** to infrastructure. Every command goes through it:
it checks who you are, decides whether you are allowed, asks someone else when
the rules say so, and writes down what happened. Kubernetes is the first room
behind that door. The plan is to add more rooms — cloud CLIs, databases, AI
agents — that all share the same login, policy, approvals, audit log, and
notifications. A customer who adopts one room is already set up for the next.

## Status legend

| Mark | Meaning |
|---|---|
| **Shipped** | In a tagged release |
| **In progress** | Merged to `master`, not yet in a release |
| **Next** | Committed to, not started |
| **Later** | Direction, not commitment |

## Shipped

Everything in the current release, `v0.2.0-alpha.1`.

| Area | What it does |
|---|---|
| Core | CLI, control plane, and per-cluster agent. Agent connects outbound over gRPC; no inbound ports, no kubeconfig distribution |
| Auth | JWT login with refresh tokens, bcrypt passwords, admin bootstrap |
| Agent tokens | Hashed at rest with a server-side pepper, one token per cluster, revocable |
| RBAC | Declarative YAML policy, hot-reloaded, glob-matched on cluster / namespace / resource / verb |
| Commands | One-shot kubectl, streaming (`logs -f`, `get -w`), interactive `exec -it` with a real PTY, and multi-port port-forward |
| Audit | Every command recorded with user, cluster, exit code, duration, client IP; retention cleanup |
| Ops | Helm charts, Dockerfiles, goreleaser, checksum-verified installer, CI and nightly e2e on Kind |

## In progress

Built, tested at every layer, merged to `master`. Waiting on the `v0.3.0-alpha.1`
release cut.

### Command guardrails

Rules that run *after* RBAC and can only take access away. Because kbridge sees
the command, not just the API call, a guardrail can tell `delete pod api-0` from
`delete pod --all`, and `apply` from `apply --dry-run`. Actions: `deny`,
`require-reason` (the `--reason` flag, stored on the audit entry), and
`require-approval`. Plus `kb policy validate` / `kb policy test` for checking a
policy file offline, in CI, before it ships.

### Just-in-time access

A `require-approval` guardrail is satisfied only by a time-boxed grant one
person requests and another approves. `kb request`, `kb grants`,
`kb admin grants approve|deny|revoke`. A pending request grants nothing; the
clock starts at approval; grants expire on their own. Self-approval is refused
by default. Every step is audited and tied to the commands it admitted.

### Grant notifications

`grants.notify` sends every grant event to Slack, Google Chat, or a signed JSON
webhook, with the approve command in the message. URL and secret can be mounted
from files or env, since a chat webhook URL is itself a credential.

## Next

Roughly the next quarter, in order.

1. **Release `v0.3.0-alpha.1`.** The three features above are a coherent story:
   who can change production, and how. Ship them.
2. **`.gitattributes`.** The repo has mixed line endings and it has cost time
   twice. One file, one commit.
3. **Break-glass.** A `require-approval` guardrail with no approver awake locks
   the on-call engineer out of production during the incident the access is
   for. Break-glass is a self-approved grant with a short window, a loud audit
   status, and a mandatory notification. Access first, review after.
4. **Grant retention.** Decided grants accumulate forever in a table that is
   read on every guarded command. Fold them into the audit cleanup job.
5. **Agent-side hard denies.** A small policy the agent enforces itself
   ("never delete a namespace, never touch kube-system"), owned by the cluster
   operator and invisible to the control plane. A compromised control plane
   cannot lift it.
6. **Signed commands and impersonation.** The user signs each command with a
   key only they hold; the agent verifies it and runs the command *as that user*
   inside Kubernetes. Approvals are signed by the approver the same way. After
   this, a compromised control plane can refuse commands but cannot forge one or
   act as anyone. This is the property that makes a hosted control plane
   trustworthy: *even if we are hacked, we cannot run a command as your users.*
7. **Approve from the chat message.** Incoming webhooks are one-way; this needs
   a Slack app with interactive components and a signed callback into the
   approve endpoint.
8. **Per-cluster notification routing.** Production requests to the SRE
   channel, dev requests elsewhere. A `clusters` filter on each hook.

## Later

Direction for the year. Order will be set by what paying users ask for.

**New rooms behind the same door**

- **AI agent gateway.** Every AI agent goes through kbridge: its own identity,
  reads allowed, writes need a human approval, every action logged with the
  prompt that caused it. Starts as an MCP server mode of the control plane that
  reuses the existing authorization path rather than adding a second one. This
  is the sentence that gets meetings in 2026.
- **Cloud CLI broker.** `kb aws …`, `kb gcloud …` through short-lived
  credentials the broker holds, with command-level rules. No cloud keys on
  laptops.
- **Database broker.** Port-forward already exists; add credential injection so
  nobody sees a database password, query rules ("no `DROP` in prod"), and
  session recording.

**Compliance and visibility**

- **Session recording.** Interactive sessions already stream through the
  control plane; persist them, searchable and tamper-evident.
- **Evidence vault.** Every session, command, approval, and agent action from
  every room in one tamper-proof store, with audit-pack exports.
- **Access inventory.** One picture of who can reach what across clusters,
  clouds, databases, and GitHub, with quarterly reviews and one-click revoke.
- **SIEM export** to Splunk, Datadog, Elastic, or syslog.

**Platform**

- **SSO (OIDC/SAML) and SCIM.** Bindings by IdP group instead of email.
  Deprovisioning in the IdP kills access instantly.
- **Web console** for audit search, session replay, and access reviews.
- **PostgreSQL store**, mutual TLS, Prometheus metrics, port-forward idle
  timeout, stateful guardrails (dry-run-then-apply within a window).
- **Incident copilot with guardrails.** An AI proposes the fix, a human
  approves, the fix runs through the door.

## What we will not build

- **Dashboards.** Monitoring, observability, and cost *dashboards* are crowded
  and cheap. kbridge acts and decides; it does not draw graphs.
- **Our own SSO or secrets manager.** Integrate with Okta, Entra, Vault. Those
  markets are won.
- **A control plane that holds customer credentials.** Credentials stay on the
  agent, inside the customer's network. The hosted service knows who asked,
  whether the answer was yes, and what they did — nothing else.
- **Rooms nobody asked for.** Build the next room when a paying user names it.

## How it becomes a business

Free for small teams with one room. Paid when they want a second room,
approvals, notifications, or the evidence vault. The same binary runs
self-hosted or cloud-managed, so regulated buyers who must self-host are sold
support and enterprise features, not a different product.
