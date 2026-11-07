# kbridge Roadmap

_Last updated 2026-11-04. The one place for what is done, what is being built,
and what is planned. How each shipped feature was built lives in the git log and
`CHANGELOG.md`._

kbridge is open source at **https://github.com/why-xn/kbridge** under the
Apache License 2.0.

## The idea in one paragraph

kbridge is a **safer kubectl**. Every command passes through it: it checks who
you are, decides whether the command is allowed, asks someone else when the
rules say so, runs the command in the cluster *as you*, and writes down what
happened in words a person can read. It is one binary, one Helm chart, and one
CLI, so a small team can have it running in fifteen minutes and never operate
it. It is the kubectl you can hand to a new engineer, a contractor, or an AI
agent.

kbridge is Kubernetes only. It is not an access platform for SSH, databases, or
cloud accounts, and it will not become one. Those products exist; kbridge is
different because it understands the *command*, not just the API call, and can
stop a mistake before it reaches the cluster.

## Status legend

| Mark | Meaning |
|---|---|
| **Shipped** | In a tagged release |
| **In progress** | Merged to `master`, not yet released |
| **Planned** | Intended; not started |

Work is organized into four tracks. Track 1 is the access path itself. Track 2
is the safety layer: deciding whether an operation should run. Track 3
makes every action attributable and its record trustworthy. Track 4 extends all
of it to AI agents. A final section lists what is table stakes and what we will
not build.

## Next up

The immediate queue, across tracks, in order.

1. **Release what is on `master`.** Guardrails, just-in-time access, policy
   tooling, and grant notifications are built and unreleased.
2. `.gitattributes`. The repo has mixed line endings and it has cost time twice.
3. **Human-readable audit.** One line per action, in words, with the reason and
   the approver; one query for "every mutation in production last week."
4. The three small guardrail additions: mutation rate limiting, dry-run before
   change, and break-glass.
5. Fleet-wide reads.
6. MCP server with scoped agent identities.

---

## Track 1: Access without credentials

Conventional Kubernetes access means handing out kubeconfig files or exposing the
API server. Both create credentials to steal and surface to attack. In kbridge
the user holds nothing: a central control plane checks permissions and forwards
the command to a lightweight agent inside the target cluster, the agent connects
outbound so no inbound port is needed, and access is revoked in one place.

**Shipped**

| Area | What it does |
|---|---|
| Core | CLI, control plane, and per-cluster agent over outbound gRPC. No inbound ports, no kubeconfig distribution |
| Auth | JWT login with refresh tokens, bcrypt passwords, admin bootstrap |
| Agent tokens | Hashed at rest with a server-side pepper, one token per cluster, revocable |
| Commands | One-shot kubectl, streaming (`logs -f`, `get -w`), interactive `exec -it` with a real PTY, multi-port port-forward |
| Ops | Helm charts, Dockerfiles, goreleaser, checksum-verified installer, CI and nightly end-to-end tests on Kind |
| Credential-less kubectl | The agent's kubectl holds no token: every request goes through a loopback proxy in the agent that owns the cluster connection, refuses connection and file flags, and keeps the agent's own namespace out of reach |
| Identity pass-through | Each command runs as `kbridge:<email>` inside Kubernetes, so cluster RBAC is a second check and the cluster audit log names the person, with a request id that matches kbridge's entry |
| RBAC sync | Policy roles say what they mean per cluster (`clusterAccess`); the agent keeps the RoleBindings in step under a chart-set ceiling and a Pod Security guard, and `kb admin rbac status` reports drift |

**Planned**

- **Agent-side hard denies.** A small policy the agent enforces itself, owned by
  the cluster operator and invisible to the control plane, so a compromised
  control plane cannot lift it. The protected-namespace list is the first one.
- **Signed commands.** The user signs each command with a key only they hold and
  the agent verifies it, so a breached control plane can refuse a command but
  cannot forge one or act as anyone. This is what makes a hosted control plane
  trustworthy.
- **Fleet-wide reads.** `kb --clusters 'prod-*' get pods`: one read across every
  cluster a user may access, results labelled by cluster.
- **Operational gaps.** Prometheus metrics, and an idle timeout for port-forward
  sessions, which currently hold the tunnel until the user interrupts them.
- **Road to stable.** The alpha series is explicit that interfaces may change.
  Reaching a stable release means freezing the config schema, the policy schema,
  and the REST paths, and shipping mutual TLS and a PostgreSQL store option so
  the deployment story fits regulated environments. Conditions, not a date.

## Track 2: Don't break things once you're in

Standing production access plus no pre-execution checks means one mistaken
command can cause an outage. Because kbridge evaluates the *requested operation*
before it reaches the cluster, rather than only proxying API calls, it can tell
`delete pod api-0` from `delete pod --all`, and `apply` from `apply --dry-run`.
It prevents incidents instead of merely recording them. No access platform does
this at the command level, and it is the reason kbridge exists.

**In progress**

- **Command guardrails.** Rules that run after RBAC and can only take access
  away. Actions today: `deny`, `require-reason` (a ticket reference recorded on
  the audit entry), and `require-approval`.
- **Just-in-time access.** A `require-approval` guardrail is satisfied only by a
  time-boxed grant one person requests and another approves. A pending request
  grants nothing, the clock starts at approval, grants expire on their own, and
  self-approval is refused by default.
- **Policy test tooling.** `kb policy validate` and `kb policy test` check a
  policy file offline and in CI, before it reaches a cluster.
- **Grant notifications.** Every grant event to Slack, Google Chat, or a signed
  JSON webhook, with the approve command in the message.

**Planned**

- **Mutation rate limiting.** A guardrail action capping how many changes one
  person may make in a window, so a runaway script or a bad loop is stopped
  rather than logged.
- **Dry run before change.** Require a server-side dry run, then admit the real
  operation within a short window. The first stateful guardrail.
- **Break-glass.** A `require-approval` guardrail with no approver awake locks
  the on-call engineer out during the incident the access is for. Break-glass is
  a self-approved grant with a short window, a loud audit status, and a mandatory
  notification. Access first, review after.
- **Preview before mutation.** Before a change runs, show what it will touch:
  "this will modify 14 objects in `prod`, continue?", built on the server-side
  dry-run diff.
- **Documented policy templates** for common shapes: protect production, require
  a ticket for mutations, guard a PCI namespace.
- **Approve from the chat message**, which needs interactive components and a
  signed callback rather than a one-way webhook.
- **Per-cluster notification routing**, so production requests reach the on-call
  channel and development requests do not.
- **Grant retention**, folded into the audit cleanup job.

## Track 3: Who did what, under whose authority

When humans and increasingly autonomous agents both perform privileged actions,
an organization has to be able to say who or what did something, under whose
authority, and whether the record can be trusted. Regulated sectors need this
first, but the need is general.

**Shipped**

- **Audit log.** Every command recorded with user, cluster, exit code, duration,
  and client IP, with retention cleanup. Guardrail refusals are recorded as
  `blocked` and approvals carry the grant that admitted them.

**Planned**

- **Human-readable audit.** "Alice deleted deployment `web` in `prod` at 14:02,
  reason: rolling back INC-4411, approved by Bob." One line per action, and one
  query for every mutation in a namespace over a period. The cluster's own
  audit log keeps the API-level detail; kbridge's is the one a team lead reads.
- **Delegation lineage.** Record the acting identity and the authority it acts
  under, so an action taken by an agent carries the human who delegated to it.
  Answering "who authorized this" is as important as "who ran it."
- **Structured audit export.** JSON lines and syslog first; adapters for
  specific platforms when a user names one.
- **Tamper-evident audit log.** Hash-chain the entries so a later edit is
  detectable. Session recording of interactive `exec` comes after this, not
  before it.
- **Access reviews with attestation.** Periodic "here is everyone who can reach
  production, please confirm" reports with one-click revoke and a signed record
  of the confirmation.

## Track 4: Governed access for AI agents

Engineering teams are already letting AI agents inspect and change
infrastructure, often by handing them long-lived credentials. That recreates the
credential, accountability, and over-privilege problems kbridge exists to solve,
at machine speed. An agent does not need SSO or a certificate; it needs a
bounded command surface, a human who is answerable for it, and a record. That is
what the three tracks above already build for people, applied to agents.

**Planned**

- **MCP server.** Expose kbridge's operations as Model Context Protocol tools so
  agents reach clusters through the same gateway as people. It runs as a mode of
  the control plane and reuses the existing authorization path rather than adding
  a second one, so a guardrail written for humans applies to agents unchanged.
- **Scoped agent identity.** Each agent is a kbridge user of its own, with a
  short-lived credential and its own policy bindings, and therefore its own
  identity in the cluster's audit log. Read-only by default; any mutation goes
  through the Track 2 approval path to a human.
- **Containment by construction.** Because authority comes from the agent's
  identity and not from anything the agent says, a compromised agent or a prompt
  injection does not by itself grant authority beyond that identity's
  permissions, approval requirements, and guardrails.
- **Audit with context.** Every agent action recorded with the prompt context
  that triggered it and the human authority it acts under, via Track 3.
- **AI-assisted troubleshooting.** The demonstration capability: an agent
  inspects Kubernetes resources and telemetry through kbridge, identifies likely
  causes, and proposes corrective actions. Any privileged change is evaluated by
  the policy engine and routed for human authorization where the policy requires
  it. Faster diagnosis without relaxing least privilege.

---

## Table stakes

Things every access tool eventually has. None of them makes kbridge different,
so each is built when a user asks for it, in roughly this order, and not before.

- **OIDC login**, so people sign in with the identity provider they already have.
  Small, and likely first.
- **SAML and SCIM provisioning**, so access ends when the organizational role
  does.
- **Device trust and per-session MFA.**
- **Session recording** of interactive sessions, replayable and searchable.
- **Anomaly alerts** on the audit stream: off-hours access, a first-time
  namespace, mass reads of secrets.

## How we will know it is working

| Track | Evidence |
|---|---|
| 1 | Tagged releases, external testers and adopters, issues and contributions from outside the project, and installs that stay installed |
| 2 | Policies adopted in pilots, and a count of destructive operations actually blocked. This is already queryable: guardrail refusals are audit entries with status `blocked`, so a pilot produces the evidence as a side effect of normal use |
| 3 | Adoption in environments that require verifiable records, including regulated sectors; audit exports accepted by an existing security platform |
| 4 | Pilot teams measuring time-to-diagnosis with agents operating under enforced least privilege, and zero privileged agent actions without an approval record |

## What we will not build

- **Rooms beyond Kubernetes.** No SSH, database, or cloud-CLI brokering. The
  same door pointed at other systems is a different product, and a crowded one.
- **Dashboards.** Monitoring, observability, cost, and audit consoles are crowded
  and cheap. kbridge acts and decides; it does not draw graphs. The audit log is
  queried from the CLI and exported to the tools that do.
- **Our own SSO or secrets manager.** Integrate with Okta, Entra, and Vault.
  Those markets are won.
- **A control plane that holds customer credentials.** Credentials stay on the
  agent, inside the customer's network. A hosted control plane knows who asked,
  whether the answer was yes, and what they did. Nothing else.
