# Admin console

The console is a read-only web view of your kbridge installation. It shows
cluster health, the audit log, agent tokens, and users. It changes nothing.

Open `https://<control-plane>/`.

## Who can use it

Only kbridge admin accounts: the account seeded from `auth.admin_*` on first
startup. The policy's `admin` role governs kubectl access only and does not
open the console. Sign in with your kbridge email and password. Anyone else is
told to use `kb login`.

The session lives in the browser tab. It ends when you close the tab, sign
out, or the refresh token expires (`auth.refresh_token_expiry`, 168h by
default). The console then asks you to sign in again.

## Screens

- **Clusters** (the landing page): each cluster's connection state, the last
  heartbeat of its connected agent, and whether its RBAC bindings match the
  policy. A disconnected cluster shows when its agent was last heard from
  (`Disconnected · heartbeat 3 min ago`), or `heartbeat never` if the control
  plane has restarted since. A cluster whose agent has never connected shows
  `Waiting for agent · no heartbeat yet` and the commands to create a token and
  install the agent. Connected clusters are listed first, then disconnected
  ones, then those waiting for an agent. The page refreshes on its own.
- **Audit Log**: every command attempt, newest first. It opens on the Last 24
  Hours; pick Any Time, Last Hour, Last 7 Days or a custom range for more.
  Filter by user, cluster, namespace, time, command text, and status with the
  tabs All, Success, Denied, Failed and Canceled; entries with status
  `timeout` appear only under All. Click a row for details, including the
  message shown to the user on a denial. The log follows new entries while you
  view the first page, and you can export the filtered rows as CSV, up to the
  first 10,000.
- **Agent Tokens**: one row per token with cluster, prefix, state, last use,
  and expiry. It opens on the Active tokens; the Revoked and All tabs show the
  rest. Secrets are never shown.
- **Users**: each account with its policy roles, the clusters and namespaces
  those roles allow, its last command, and its denied commands in the last
  seven days. A role that lists the `auth` verb shows as `read-write`, because
  `kubectl auth reconcile` writes RBAC. The default `viewer` lists `auth` for
  `kb auth whoami`, so it shows `read-write` although the cluster's `view`
  role refuses every write.

The sidebar has a switch between the dark theme (the default) and the light
one. The choice is kept in the browser.

## Security

The console loads nothing from outside the control plane: no CDN, fonts, or
analytics. Its pages are served with a Content-Security-Policy that allows
only same-origin scripts, styles, images, and requests, and forbids framing.
It calls the same admin API as `kb admin`, with your own token.

## Turn it off

Set `ui.enabled: false` in `control-plane.yaml`, `KBRIDGE_UI_ENABLED=false`,
or `ui.enabled=false` in the control plane chart. `/` then returns 404. See
[configuration](configuration.md).

The API behind it is in the [API reference](api.md). The same tasks are
available from the CLI: see the [admin guide](admin.md).
