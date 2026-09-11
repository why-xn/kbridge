# kbridge Roadmap

_Last updated 2026-09-11. The one place for what is done, what is being built,
and what is planned. How each shipped feature was built lives in the git log and
`CHANGELOG.md`._

kbridge is open source at **https://github.com/why-xn/kbridge** under the
Elastic License 2.0.

## The idea in one paragraph

kbridge is a **front door** to infrastructure. Every command passes through it:
it checks who you are, decides whether you are allowed, asks someone else when
the rules say so, and writes down what happened. Kubernetes is the first room
behind that door. Later rooms, cloud CLIs and databases and AI agents, share the
same login, policy, approvals, audit log, and notifications, so a team that
adopts one is already set up for the next.

## Status legend

| Mark | Meaning |
|---|---|
| **Shipped** | In a tagged release |
| **In progress** | Merged to `master`, not yet released |
| **Planned** | Intended; not started |

Work is organized into five tracks. Track 1 is the access path itself. Track 2
decides whether an operation should run. Track 3 makes every action attributable
and its record trustworthy. Track 4 extends all of it to AI agents. Track 5
points the same door at systems beyond Kubernetes.

## Next up

The immediate queue, across tracks, in order.

1. Identity pass-through (impersonation) with delegation lineage. One piece of
   work that serves both Track 1 and Track 3, which makes it the highest-leverage
   item on this list.
2. The three small guardrail additions: mutation rate limiting, dry-run before
   change, and break-glass. Under two weeks together.
3. `.gitattributes`. The repo has mixed line endings and it has cost time twice.
4. Grant retention, folded into the existing audit cleanup job.
5. Fleet-wide operations.
6. MCP server with scoped agent identities, then AI-assisted troubleshooting on
   top of it.

---

## Track 1: Credential-free, identity-based access

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

**Planned**

- **Identity pass-through.** The agent runs each command *as the requesting
  user* inside Kubernetes, so the cluster's own audit log names the real person
  rather than the agent's service account. The user signs each command with a key
  only they hold and the agent verifies it, which means a compromised control
  plane can refuse a command but cannot forge one or act as anyone. This is what
  makes a hosted control plane trustworthy: even if the control plane is
  breached, it cannot run a command as your users.
- **Fleet-wide operations.** Run one read across every cluster a user may
  access, with results aggregated and labelled by cluster, plus inventory and
  drift reports over the fleet.
- **Agent-side hard denies.** A small policy the agent enforces itself, owned by
  the cluster operator and invisible to the control plane, so a compromised
  control plane cannot lift it.
- **Operational gaps.** Prometheus metrics, and an idle timeout for port-forward
  sessions, which currently hold the tunnel until the user interrupts them.
- **Road to stable.** The alpha series is explicit that interfaces may change.
  Reaching a stable release means freezing the config schema, the policy schema,
  and the REST paths, and shipping mutual TLS and a PostgreSQL store option so
  the deployment story fits regulated environments. Conditions, not a date.

## Track 2: Just-in-time authorization and preventive guardrails

Standing production access plus no pre-execution checks means one mistaken
command can cause an outage. Because kbridge evaluates the *requested operation*
before it reaches the cluster, rather than only proxying API calls, it can
prevent incidents instead of merely recording them. This is security-as-code
applied to operations.

**In progress**

- **Command guardrails.** Rules that run after RBAC and can only take access
  away. Because kbridge sees the command, a guardrail can tell `delete pod api-0`
  from `delete pod --all`, and `apply` from `apply --dry-run`. Actions today:
  `deny`, `require-reason` (a ticket reference recorded on the audit entry), and
  `require-approval`.
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
- **Grant retention**, folded into the audit cleanup job.
- **Documented policy templates** for common shapes: protect production, require
  a ticket for mutations, guard a PCI namespace.
- **Approve from the chat message**, which needs interactive components and a
  signed callback rather than a one-way webhook.
- **Per-cluster notification routing**, so production requests reach the on-call
  channel and development requests do not.

## Track 3: Verifiable attribution and tamper-evident audit

When humans and increasingly autonomous agents both perform privileged actions,
an organization has to be able to say who or what did something, under whose
authority, and whether the record can be trusted. Regulated sectors need this
first, but the need is general.

**Shipped**

- **Audit log.** Every command recorded with user, cluster, exit code, duration,
  and client IP, with retention cleanup. Guardrail refusals are recorded as
  `blocked` and approvals carry the grant that admitted them.

**Planned**

- **Delegation lineage.** Record the acting identity and the authority it acts
  under, so an action taken by an agent carries the human who delegated to it.
  Answering "who authorized this" is as important as "who ran it."
- **Tamper-evident session recording.** Interactive sessions already stream
  through the control plane. Persist them as replayable recordings, searchable by
  user, cluster, and command, hash-chained so a later edit is detectable.
- **Audit console.** A web view that answers who touched which system and when,
  with session replay, for the security and compliance people who do not live in
  a terminal.
- **Access reviews with attestation.** Periodic "here is everyone who can reach
  production, please confirm" reports with one-click revoke and a signed record
  of the confirmation.
- **Enterprise identity integration.** SSO via OIDC and SAML, plus SCIM
  provisioning, so policy binds to organizational roles and access ends when the
  role does.
- **Structured audit export** to standard security platforms: Splunk, Datadog,
  Elastic, or plain syslog.
- **Access inventory.** One picture of who can reach what across clusters, cloud
  accounts, databases, and source control, so an access review starts from
  reality rather than a spreadsheet.
- **Anomaly detection on the audit stream.** Off-hours access, a first-time
  namespace, mass reads of secrets, a sudden spike in mutations. Alerts reuse the
  notification path already built for grants.
- **Evidence vault.** One tamper-proof store for sessions, commands, approvals,
  and agent actions across every room, with audit-pack exports.

## Track 4: Governed access for AI agents

Engineering teams are already letting AI agents inspect and change
infrastructure, often by handing them long-lived credentials. That recreates the
credential, accountability, and over-privilege problems kbridge exists to solve,
at machine speed. The governance layer is the point; AI-assisted troubleshooting
is the use case that demonstrates it.

**Planned**

- **MCP server.** Expose kbridge's operations as Model Context Protocol tools so
  agents reach clusters through the same gateway as people. It runs as a mode of
  the control plane and reuses the existing authorization path rather than adding
  a second one, so a guardrail written for humans applies to agents unchanged.
- **Scoped agent identity.** Each agent gets its own identity with a short-lived
  credential and its own policy bindings. Read-only by default; any mutation goes
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

## Track 5: Other rooms behind the same door

Kubernetes is not the only thing engineers reach into with long-lived
credentials. The tracks above build the door; this one applies it to the next
rooms, reusing one login, one policy engine, one approval path, and one audit
log, so a team that already governs cluster access gets the rest without a second
system to run. Sequenced by what users ask for.

**Planned**

- **Cloud CLI broker.** `kb aws …`, `kb gcloud …`, `kb az …` run against
  short-lived credentials the broker holds, never keys on a laptop, with
  command-level guardrails so deleting a production bucket needs the same
  approval a production namespace does.
- **Database broker.** The port-forward tunnel already exists. Add credential
  injection so an engineer connects to a database without ever seeing its
  password, query-level guardrails, and session recording for the same
  attribution story as Track 3.

---

## How we will know it is working

| Track | Evidence |
|---|---|
| 1 | Tagged releases, external testers and adopters, issues and contributions from outside the project |
| 2 | Policies adopted in pilots, and a count of destructive operations actually blocked. This is already queryable: guardrail refusals are audit entries with status `blocked`, so a pilot produces the evidence as a side effect of normal use |
| 3 | Adoption in environments that require verifiable records, including regulated sectors; audit exports accepted by an existing security platform |
| 4 | Pilot teams measuring time-to-diagnosis with agents operating under enforced least privilege, and zero privileged agent actions without an approval record |
| 5 | Teams governing a second system through kbridge, and long-lived cloud or database credentials retired as a result |

## What we will not build

- **Dashboards.** Monitoring, observability, and cost dashboards are crowded and
  cheap. kbridge acts and decides; it does not draw graphs.
- **Our own SSO or secrets manager.** Integrate with Okta, Entra, and Vault.
  Those markets are won.
- **A control plane that holds customer credentials.** Credentials stay on the
  agent, inside the customer's network. A hosted control plane knows who asked,
  whether the answer was yes, and what they did. Nothing else.
- **Rooms nobody asked for.** Build the next room when a user names it.
