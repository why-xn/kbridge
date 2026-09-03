# kbridge — Feature Brainstorm (Founder's View)

_Generated 2026-09-03. A ranked list of features that would add real, sellable
value, framed as a startup founder would look at the market._

## The positioning problem

kbridge is in a crowded space: Teleport, StrongDM, Tailscale, and HashiCorp
Boundary all do "secure access to Kubernetes." They are well-funded and
enterprise-ready. Competing head-on on *access* is a losing game.

But kbridge has one structural thing they don't: **it sits at the command
level, not the API-server level.** Teleport proxies the K8s API and delegates
to Kubernetes RBAC. kbridge already parses `verb / resource / namespace` out of
each kubectl invocation (`internal/controlplane/policy.go`) and sees every byte
of every PTY session. That's the wedge — it can reason about *intent*, not just
*connections*.

The category to own: **"the control layer for who — and what — can change
your clusters."** Where "what" increasingly means AI agents.

## Tier 1 — Must-haves to close any enterprise deal

These aren't differentiators; without them the security team says no before
the demo ends.

1. **SSO (OIDC/SAML) + SCIM provisioning.** Okta, Entra ID, Google Workspace.
   Bindings in `rbac.yaml` become IdP groups instead of emails.
   Deprovisioning in the IdP kills access instantly. Every enterprise buyer
   asks this in the first ten minutes.

2. **Session recording and replay.** Every interactive `exec -it` already
   streams through the control plane — persist it as asciicast, make it
   searchable by user/cluster/pod/command, tamper-evident (hash chain).
   SOC 2, PCI-DSS, and HIPAA auditors ask for this by name. This is the single
   feature StrongDM built their pricing around.

3. **SIEM export.** Ship the audit log to Splunk, Datadog, Elastic, or plain
   webhook/syslog. Checkbox feature, but a hard blocker if missing.

4. **Web console.** Security and compliance people don't live in a terminal.
   Audit search, session replay viewer, access review reports. Doesn't need to
   be a Lens clone — it needs to answer "who touched prod last Tuesday" in
   three clicks.

## Tier 2 — Why they'd pick kbridge over Teleport

5. **Just-in-time access with approval workflows.** Nobody has standing prod
   access. `kb request prod --role deployer --duration 2h --reason "INC-4521"`
   → Slack/Teams message to an approver → approve → time-boxed role binding,
   auto-revoked. Optional break-glass with post-hoc review. This converts
   kbridge from "access tool" to "least-privilege enforcement," which is where
   the budget lives. The policy engine already hot-reloads — this adds a time
   dimension and an approval state machine.

6. **Command guardrails (policy-as-code).** Because kbridge parses intent, it
   can do things API-proxies can't:
   - Block `delete namespace` / `delete --all` in clusters matching `prod-*`
   - Require `--dry-run=server` before `apply` in prod, then allow the real one
     within 5 minutes
   - Require a ticket ID / reason on any mutating verb in prod
   - Rate-limit mutations per user per hour
   - Deny `exec` into pods labeled `pci=true` outside approved windows

   Expressed in CEL or Rego, tested with `kb policy test`. The sales pitch is
   "we prevent the outage, not just log it." Every SRE has a story about a
   fat-fingered delete.

7. **End-to-end attribution via impersonation.** The agent currently runs
   kubectl as its own service account, so the cluster's native audit log says
   "kbridge-agent did it." Pass the real identity through with
   `--as=alice@corp.com --as-group=...`. Then the Kubernetes audit log,
   kbridge's audit log, and the session recording all agree on who did what.
   Compliance teams care deeply about this and it's a small change.

8. **AI-agent access gateway.** The one to bet the company on. Every
   engineering org is letting Copilot/Claude/internal agents touch
   infrastructure right now, and they're doing it by handing the agent a
   kubeconfig. That's a governance nightmare. The MCP server is already on the
   roadmap — make it the headline product:
   - Each AI agent gets its own identity with a scoped, short-lived token
   - Read-only by default; any mutation requires a human approval (feature 5,
     reused)
   - Every tool call is audited with the *prompt context* that triggered it
   - Guardrails (feature 6) apply identically to humans and agents

   Pitch: "Let your AI agents debug production without letting them break
   it." There's no incumbent in this exact niche yet, and it reuses
   `authorizeExec` and `AuditRecorder` — no second authz path.

## Tier 3 — Expansion (bigger TAM, same architecture)

9. **Credential brokering for databases.** Port-forward already exists. Add a
   mode where the agent injects DB credentials from Vault / a secrets store,
   so the user runs `kb db connect orders-prod` and gets a psql session
   without ever seeing a password. Rotate credentials transparently. This is
   StrongDM's entire business, nearly free because the tunnel exists.

10. **Fleet-wide kubectl.** `kb --all-clusters get pods -l app=payments`
    fanning out to every cluster the user can access, aggregated with a
    cluster column. Add inventory/drift reports ("which clusters run image X
    with CVE Y"). Sticky daily-use feature — not what closes the deal, but
    what stops churn.

11. **Access reviews and attestation.** Quarterly "here's everyone who can
    reach prod, manager please confirm" reports with one-click revoke. Boring,
    and buyers pay for boring compliance automation every year.

12. **Anomaly detection on the audit stream.** Off-hours access, first-time
    access to a namespace, mass reads of secrets, sudden mutation spikes.
    Alert to Slack/PagerDuty/SIEM. Cheap once the audit log is structured,
    and it makes the product look alive.

## Recommended build order

1. **SSO + session recording** (Tier 1 blockers, ~2 months) — makes the
   product sellable at all.
2. **JIT approvals + guardrails** (~2 months) — the differentiator and the
   price justification.
3. **AI-agent gateway** — the story that gets press, design partners, and a
   wedge into companies that already have Teleport.
4. **Web console** throughout, minimal at first.

Everything else waits for a customer to ask for it.

## Pricing instinct

Per-seat for humans, per-agent-identity for AI, clusters unlimited. Free tier
up to 5 users / 3 clusters so platform teams adopt bottom-up, then the
security team upgrades for recording, SSO, and approvals.
