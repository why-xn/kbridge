# RBAC Policy Reference

kbridge authorization is **declarative**: a single YAML policy file defines roles
and who they apply to. The file is pointed to by `rbac.policy_file` in
`control-plane.yaml`. A change to the mounted file, or a `SIGHUP`, reloads it in
place within 10 s of reaching the disk; a change through Helm restarts the
control plane, and `kb` fails for about a minute while the old pod stops and
the agent reconnects — see
[Reloading](#reloading) below. When `rbac.policy_file` is empty, enforcement is
disabled and every authenticated user is allowed.

Authentication (who you are) is separate from authorization (what you can do):
identity comes from the JWT; permissions come from this file.

## Structure

```yaml
default: viewer          # optional: role for any user with no matching binding

roles:
  - name: <role-name>
    rules:
      - clusters:   ["<pattern>", ...]
        namespaces: ["<pattern>", ...]
        resources:  ["<pattern>", ...]
        verbs:      ["<verb>", ...]   # or ["*"]
    clusterAccess:                    # optional: what the agent binds in the cluster
      - clusters:    ["<pattern>", ...]
        clusterRole: <ClusterRole name>
        namespaces:  ["<namespace>", ...]   # or clusterWide: true, not both

bindings:
  - subject: <email-or-pattern>   # matched against the JWT email
    roles: ["<role-name>", ...]
```

A request is **allowed if any rule of any of the user's roles matches**. A rule
matches when the request's cluster, namespace, and resource each match at least
one pattern in the corresponding list, and the verb is in `verbs` (or `verbs`
contains `*`).

### Patterns

`*` is a wildcard matching any sequence of characters. Examples:
`*` (anything), `dev-*` (matches `dev-cluster`), `*-prod`, `app-*-svc`.
Subjects support the same wildcards, e.g. `*@dev.corp.com`.

### How a kubectl command maps to a request

| Part | Derived from |
|------|--------------|
| cluster | the target cluster (`kb clusters use`) |
| verb | the first kubectl arg (`get`, `delete`, `apply`, …) |
| resource | the resource type; `pods` for `logs`/`exec`/`cp`/etc.; `foo/name` → `foo` |
| namespace | `-n`/`--namespace`; `*` for `-A`/`--all-namespaces`; else `default` |

A flag that takes a value must be written `--flag=value` when it comes before
the resource, or be placed after it: kbridge reads an unknown `--flag value`
before the resource as taking a value, so `kb get --sort-by pods secrets` is
checked as `secrets`, and `kb get --no-headers pods` has no resource and is
refused by a role that lists resources.

## Example

```yaml
default: viewer

roles:
  - name: admin
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["*"]
    clusterAccess:
      - clusters: ["*"]
        clusterRole: cluster-admin
        clusterWide: true

  - name: developer
    rules:
      - clusters: ["dev-*", "staging"]
        namespaces: ["*"]
        resources: ["pods", "deployments", "services", "configmaps"]
        verbs: ["get", "list", "watch", "describe", "logs", "exec", "apply"]

  - name: viewer
    rules:
      - clusters: ["*"]
        namespaces: ["*"]
        resources: ["*"]
        verbs: ["get", "list", "watch", "describe", "logs"]
    clusterAccess:
      - clusters: ["*"]
        clusterRole: view
        clusterWide: true

bindings:
  - subject: admin@corp.com
    roles: ["admin"]
  - subject: "*@dev.corp.com"
    roles: ["developer"]
```

With this policy: `admin@corp.com` can do anything; anyone at `dev.corp.com`
gets developer access on dev/staging; everyone else falls back to read-only
`viewer`. The chart's default policy and `configs/rbac.yaml` give `admin` a
cluster-wide `cluster-admin` binding and `viewer` a cluster-wide `view` binding,
so a fresh install works without edits.

## Cluster bindings (`clusterAccess`)

`rules` decide what kbridge lets a user ask for. The cluster then checks the
same request again as the user `kbridge:<email>` (see
[security.md](security.md#two-layer-authorization)), so that user also needs
Kubernetes RBAC. `clusterAccess` says what that RBAC is, and each cluster's
agent creates and maintains it.

```yaml
roles:
  - name: developer
    rules:
      - clusters: ["*"]
        namespaces: ["team-a"]
        resources: ["*"]
        verbs: ["*"]
    clusterAccess:
      - clusters: ["dev-*"]
        clusterRole: edit
        namespaces: ["team-a"]
  - name: viewer
    rules: [...]
    clusterAccess:
      - clusters: ["*"]
        clusterRole: view
        clusterWide: true
```

| Field | Meaning |
|-------|---------|
| `clusters` | Patterns (with `*`) for the clusters this entry applies to |
| `clusterRole` | An existing ClusterRole to bind, such as `view` or `edit`. Required |
| `namespaces` | Literal namespace names (no patterns). One RoleBinding per namespace |
| `clusterWide` | `true` for one ClusterRoleBinding instead. Exactly one of `namespaces` or `clusterWide` must be set; the policy fails to load otherwise |

What the agent creates:

- **Name:** `kbridge-<role>-<clusterRole>`, e.g. `kbridge-developer-edit` as a
  RoleBinding in `team-a` and `kbridge-viewer-view` as a ClusterRoleBinding.
  The name must be a valid Kubernetes name, so a role name with capitals or
  underscores, or a ClusterRole such as `system:...`, is refused by the agent
  (`name is not a valid kubernetes name`).
- **Subjects:** the users `kbridge:<email>` of every active kbridge user who
  holds the role, through a binding or `default`. Patterns in `bindings` match
  users that exist in kbridge; creating, disabling or deleting a user changes
  the binding. Users who hold the role through `default` get its
  `clusterAccess` too, so keep the default role's `clusterAccess` narrow.
- **Label:** `app.kubernetes.io/managed-by=kbridge`. The agent deletes labelled
  `kbridge-` bindings that policy no longer asks for, and never touches a
  binding without the label (an unlabelled one with the same name is reported
  as `exists and is not managed by kbridge`).

What the agent refuses, reported per binding in `kb admin rbac status`:

- any namespace it protects: its own (release) namespace and the chart's
  `protectedNamespaces` (`namespace "<ns>" is reserved for the kbridge agent`);
- a name without the `kbridge-` prefix, or a subject that is not a `kbridge:`
  user;
- anything above the chart's `accessCeiling`: Kubernetes answers `403` for a
  ClusterRole, namespace or cluster-wide binding the agent's ServiceAccount
  may not grant. See [operations.md](operations.md#rbac-sync);
- any ClusterRole other than `view` in a namespace that does not enforce Pod
  Security `baseline` or `restricted`. A role that can create pods could mount
  the node's disk with a `hostPath` pod and read the agent's token there. The
  agent reads the namespace's `pod-security.kubernetes.io/enforce` label before
  it writes the binding; `privileged`, no label, or a level set only on `warn`
  or `audit` is refused with
  `namespace "<ns>" must enforce pod-security baseline or restricted before <clusterRole> can be granted there`.
  Label the namespace and the agent binds on its next sync (within the chart's
  `sync.interval`, 60s by default):

  ```bash
  kubectl label ns <ns> pod-security.kubernetes.io/enforce=baseline
  ```

  If a namespace stops enforcing, the agent deletes the binding it made there
  on its next sync and reports the same refusal; so does a namespace the agent
  may not read (403) or that is gone (404). Any other read failure, such as an
  API server outage, leaves the binding as it is and reports the error.

  `view` is exempt: it cannot create pods. Cluster-wide bindings are not
  checked; see [security.md](security.md#known-limits).

A ClusterRoleBinding covers every namespace, including the protected ones; the
agent's proxy still refuses every request into those.

## Guardrails

Role rules answer "may this person touch this resource at all?". Guardrails
answer a narrower question: "is *this particular command* one we want run right
now?" They are evaluated **after** the role rules and can only take access away,
never grant it, so a command a role does not permit is denied before any
guardrail is consulted.

Guardrails exist because kbridge sees the command, not just the API call. It can
tell `delete pod api-0` from `delete pod --all`, and `apply` from
`apply --dry-run=server`, which cluster-side RBAC cannot express.

```yaml
guardrails:
  - name: no-prod-namespace-delete     # required, unique
    description: Optional prose for humans reading the file.
    match:
      clusters:   ["prod-*"]           # omit a field to match anything
      namespaces: ["*"]
      resources:  ["namespaces", "ns"]
      verbs:      ["delete"]
      args:       ["--all"]            # every pattern must match some argument
      args_not:   ["--dry-run*"]       # no pattern may match any argument
    action: deny                       # deny | require-reason
    message: "deleting namespaces in production is not allowed"
    exempt: ["breakglass@corp.com"]    # subject patterns that skip this rule
```

**Evaluation order.** Guardrails are checked in file order and the **first one
that matches decides**. Put the most specific first.

**Empty means any.** This is the opposite of a role rule, where an empty list
grants nothing. A guardrail that names only `verbs` applies to every cluster,
namespace, and resource — which is usually what you want, but check your
`clusters` scoping before deploying a broad one.

### Actions

| Action | Effect |
|---|---|
| `deny` | The command is refused. `403`, audit status `blocked`. |
| `require-reason` | Refused **unless** the caller supplied `--reason`. With a reason it runs, and the reason is stored on the audit entry. |
| `require-approval` | Refused **unless** someone else has approved a time-boxed grant covering it. See [Just-in-time access](#just-in-time-access). |

A reason is self-asserted; an approval is not. Use `require-reason` where you
want the record, and `require-approval` where you want a second pair of eyes.

A reason must be at least 8 characters after trimming (so `INC-4521` qualifies)
and is truncated at 512. Users supply it with the `--reason` flag, which kbridge
strips before the command reaches kubectl:

```bash
kb delete pod api-0 --reason "INC-4521 rolling back bad deploy"
kb apply -f app.yaml --reason="scaling for the launch"
```

The flag works on every command path: one-shot, streaming, `exec -it`,
`port-forward`, and `edit`.

### Matching on arguments

`args` and `args_not` match raw command tokens with the same `*` wildcards used
elsewhere. A token written as `--flag=value` matches both the whole token and
the bare `--flag`, so `args_not: ["--dry-run"]` catches `--dry-run=server`.

Use `args` to catch a dangerous *form* of an otherwise ordinary verb, and
`args_not` to carve out a safe one:

```yaml
  # delete is fine; delete --all is not
  - name: no-bulk-delete
    match:
      clusters: ["prod-*"]
      verbs: ["delete"]
      args: ["--all"]
    action: deny

  # mutations need a reason, but a server-side dry run changes nothing
  - name: prod-writes-need-a-reason
    match:
      clusters: ["prod-*"]
      verbs: ["apply", "delete", "edit", "patch", "scale"]
      args_not: ["--dry-run*"]
    action: require-reason
```

### Exemptions

`exempt` lists subject patterns (same wildcards as bindings) that skip the
guardrail. A break-glass identity still produces a full audit trail — it is
exempt from the block, not from the record.

### Testing a policy before you ship it

`kb policy` reads a policy file directly, with no control plane involved, so it
runs in CI against a proposed change:

```bash
# structural check: unknown roles, duplicate names, bad actions
kb policy validate -f configs/rbac.yaml

# ask how one command would be ruled on
kb policy test -f configs/rbac.yaml -u alice@corp.com -c prod-eu -- delete ns payments
```

`test` exits `0` when the command would be allowed and `1` otherwise, so it can
gate a merge. It prints the parsed request alongside the verdict, which is the
fastest way to see why a guardrail did or did not fire.

### Resource spelling

Guardrail `resources` matching tolerates the singular and plural forms kubectl
accepts, so a guardrail written for `deployments` also catches
`delete deployment api`. Irregular names can still be listed explicitly.

This widening applies to **guardrails only**. Role rules stay exact, because
there a loose match would grant more than the author spelled out, whereas a
guardrail can only take access away.

### Known limitation

The resource is parsed as the first non-flag token after the verb, so
`apply -f app.yaml` reports its resource as `app.yaml`. Scope guardrails on
`apply` by verb and cluster rather than by `resources`.

## Just-in-time access

A `require-approval` guardrail is satisfied only by an **approved, unexpired
grant**: a time-boxed permission one person requests and another approves.

```
kb request  ──▶  pending  ──▶  kb admin grants approve  ──▶  approved
                                                                │
                                              expires on its own │ or kb admin grants revoke
                                                                ▼
                                                          no longer admits
```

### Asking for access

```bash
kb request prod-eu --duration 2h --reason "INC-4521 rolling back bad deploy"
kb request prod-eu --namespace payments --reason "INC-4521 investigating"
kb grants                    # your requests and how long each has left
```

A request needs a reason of at least 8 characters. Omitting `--duration` uses
`grants.default_duration`; anything above `grants.max_duration` is refused. A
pending request grants nothing, and carries no expiry — the clock starts at
approval, not at request time.

### Deciding

```bash
kb admin grants list --status pending
kb admin grants approve <id> --note "paged, go ahead"
kb admin grants approve <id> --duration 30m     # shorten the window
kb admin grants deny <id> --note "use the runbook instead"
kb admin grants revoke <id>                     # end an approved grant early
```

Approving your own request is refused unless `grants.allow_self_approval` is on.
A grant can be decided once; a second decision returns `409`. Revocation takes
effect immediately, and works on a pending grant too.

### Scope

A grant covers a cluster and, optionally, one namespace. Both are glob patterns,
so a grant for `prod-*` covers every production cluster while one for `prod-eu`
with namespace `payments` covers nothing else.

### Configuration

```yaml
grants:
  max_duration: 8h              # ceiling on any single grant
  default_duration: 1h          # used when a request names no duration
  allow_self_approval: false    # a second pair of eyes is the point
```

Both durations are optional: leaving them unset falls back to these values, so a
config written before just-in-time access existed keeps working.

### Notifications

A request nobody sees is a request nobody approves. `grants.notify` lists
webhooks that receive every lifecycle event, so a new request lands in the
channel where approvers already are:

```yaml
grants:
  notify:
    - url: https://hooks.slack.com/services/T000/B000/xxxx
      format: slack
    - url: https://chat.googleapis.com/v1/spaces/AAA/messages?key=...
      format: google-chat
    - url: https://automation.example.com/kbridge
      format: json
      secret: "..."                 # HMAC-SHA256 of the body in X-Kbridge-Signature
      events: [grant-requested]     # default: all four events
```

| Format | Sends | Use for |
|---|---|---|
| `slack` | `text` + `blocks`, mrkdwn | Slack incoming webhooks |
| `google-chat` | `text` | Google Chat incoming webhooks |
| `json` | the structured `GrantEvent` | Teams via Power Automate, PagerDuty, your own automation |

The chat message for a new request ends with the exact `kb admin grants approve
<id>` command, so acting on it is a paste. Decision events name who decided and
their note.

Delivery is asynchronous and never fails the request that triggered it: a
timeout is 5 seconds, a failure is retried once, and both are logged. In-flight
deliveries are flushed on shutdown.

For `json` hooks with a `secret`, verify the sender by recomputing the HMAC:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
ok := hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(r.Header.Get("X-Kbridge-Signature")))
```

Chat webhooks cannot check a signature, so none is sent to them.

**Keep the URL out of the config file.** A Slack or Google Chat webhook URL is a
credential: anyone holding it can post to the channel. Both `url` and `secret`
accept a `_file` variant, and an env override indexed by the hook's position,
with the same precedence as every other secret (`_FILE` env, then env, then
file, then inline):

```yaml
grants:
  notify:
    - url_file: /run/secrets/slack-webhook-url
      format: slack
    - url: https://automation.example.com/kbridge
      format: json
      secret_file: /run/secrets/kbridge-notify-secret
```

```bash
KBRIDGE_GRANTS_NOTIFY_0_URL_FILE=/run/secrets/slack-webhook-url
KBRIDGE_GRANTS_NOTIFY_1_SECRET=...
```

Approving from inside the chat message is not yet supported: incoming webhooks
are one-way. That needs a Slack app with interactive components, which is the
natural follow-up.

### Audit

Every step is recorded: `grant-requested`, `grant-approved`, `grant-denied`, and
`grant-revoked`, each carrying the grant ID. Commands a grant admitted carry the
same `grant_id`, so `GET /api/v1/admin/audit?grant_id=<id>` returns the request,
the decision, and everything run under it.

## Reloading

A change to the file is reloaded within 10 s of reaching the disk, with no
restart:

- **File watch and poll** — the control plane reloads on a filesystem event for
  the policy file, and also re-reads the file every 10 s and reloads when its
  content changed. The poll covers what events miss: Kubernetes ConfigMap
  mounts (updated by a symlink swap) and filesystems such as 9p/NFS or WSL
  `/mnt/*` that do not deliver inotify events. Kubernetes takes up to a minute
  to copy an edited ConfigMap into the pod; the reload follows once it lands.
- **Helm** — the control-plane chart renders `rbac.policy` into a ConfigMap,
  and a `helm upgrade` that changes `rbac.policy` restarts the control plane
  (`Recreate`), so the new policy applies once the new pod is ready; `kb`
  fails for about a minute in between (the old pod's shutdown grace, then the
  agent reconnecting).
- **SIGHUP** — sending `SIGHUP` to the control plane process reloads the policy
  at once from the file as it is on disk (it does not hurry a ConfigMap edit):

  ```bash
  kill -HUP "$(pidof kbridge-control-plane)"
  ```

A reload that fails to parse or validate is logged and the previous policy
stays active, so a bad edit never takes down enforcement.

## Operational notes

- Denied commands return `403` and are recorded in the audit log with status
  `denied` — useful for spotting over-broad expectations. Commands a guardrail
  stopped are recorded as `blocked`, which separates "you never had this access"
  from "you have it, but not for this command".
- Validation runs at load time: a binding or `default` that names an undefined
  role is rejected, as is a guardrail with no name, a duplicate name, or an
  action other than `deny` or `require-reason`.
- Keep `default` least-privilege (or omit it to deny unbound users entirely).
- A policy change reaches each cluster within one agent heartbeat (30s) plus a
  reconcile; `kb admin rbac status` shows when it has.
