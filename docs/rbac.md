# RBAC Policy Reference

kbridge authorization is **declarative**: a single YAML policy file defines roles
and who they apply to. The file is pointed to by `rbac.policy_file` in
`control-plane.yaml` and is **hot-reloaded** (no restart needed) — see
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

bindings:
  - subject: admin@corp.com
    roles: ["admin"]
  - subject: "*@dev.corp.com"
    roles: ["developer"]
```

With this policy: `admin@corp.com` can do anything; anyone at `dev.corp.com`
gets developer access on dev/staging; everyone else falls back to read-only
`viewer`.

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

The policy is reloaded by two mechanisms, whichever fires first:

- **File watch** — control plane watches the policy file's directory and reloads
  automatically on change. Note this relies on filesystem change events
  (inotify), which some filesystems (e.g. 9p/NFS or WSL `/mnt/*` mounts) do not
  deliver reliably.
- **SIGHUP** — sending `SIGHUP` to the control plane process reloads the policy on
  demand. This always works, and is the recommended trigger in environments
  where the file watch may not fire:

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
