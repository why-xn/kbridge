# RBAC Policy Reference

kbridge authorization is **declarative**: a single YAML policy file defines roles
and who they apply to. The file is pointed to by `rbac.policy_file` in
`central.yaml` and is **hot-reloaded** (no restart needed) — see
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

## Reloading

The policy is reloaded by two mechanisms, whichever fires first:

- **File watch** — central watches the policy file's directory and reloads
  automatically on change. Note this relies on filesystem change events
  (inotify), which some filesystems (e.g. 9p/NFS or WSL `/mnt/*` mounts) do not
  deliver reliably.
- **SIGHUP** — sending `SIGHUP` to the central process reloads the policy on
  demand. This always works, and is the recommended trigger in environments
  where the file watch may not fire:

  ```bash
  kill -HUP "$(pidof kbridge-central)"
  ```

A reload that fails to parse or validate is logged and the previous policy
stays active, so a bad edit never takes down enforcement.

## Operational notes

- Denied commands return `403` and are recorded in the audit log with status
  `denied` — useful for spotting over-broad expectations.
- Validation runs at load time: a binding or `default` that names an undefined
  role is rejected.
- Keep `default` least-privilege (or omit it to deny unbound users entirely).
