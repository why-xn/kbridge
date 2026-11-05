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
  may not grant. See [operations.md](operations.md#rbac-sync).

A ClusterRoleBinding covers every namespace, including the protected ones; the
agent's proxy still refuses every request into those.

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
  `denied` — useful for spotting over-broad expectations.
- Validation runs at load time: a binding or `default` that names an undefined
  role is rejected.
- Keep `default` least-privilege (or omit it to deny unbound users entirely).
- A policy change reaches each cluster within one agent heartbeat (30s) plus a
  reconcile; `kb admin rbac status` shows when it has.
