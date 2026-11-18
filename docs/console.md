# Admin console

The console is a read-only web view of your kbridge installation. It shows
cluster health, the audit log, agent tokens, and users. It changes nothing.

Open `https://<control-plane>/ui/`. The root path `/` redirects there.

## Who can use it

Only users with the `admin` role. Sign in with your kbridge email and
password. Anyone else is told to use `kb login`.

The session lives in the browser tab. It ends when you close the tab, sign
out, or the refresh token expires (`auth.refresh_token_expiry`, 168h by
default). The console then asks you to sign in again.

## Screens

- **Clusters** (the landing page): each cluster's connection state, last
  heartbeat, and whether its RBAC bindings match the policy. A cluster with no
  agent yet shows the commands to create a token and install the agent. The
  page refreshes on its own.
- **Audit Log**: every command attempt, newest first. Filter by user, cluster,
  namespace, status, time, and command text. Click a row for details, including
  the message shown to the user on a denial. The log follows new entries while
  you view the first page, and you can export the filtered rows as CSV.
- **Agent Tokens**: one row per token with cluster, prefix, state, last use,
  and expiry. Secrets are never shown.
- **Users**: each account with its policy roles, the clusters and namespaces
  those roles allow, its last command, and its denied commands in the last
  seven days.

The sidebar has a switch between the dark theme (the default) and the light
one. The choice is kept in the browser.

## Security

The console loads nothing from outside the control plane: no CDN, fonts, or
analytics. Its pages are served with a Content-Security-Policy that allows
only same-origin scripts, styles, images, and requests, and forbids framing.
It calls the same admin API as `kb admin`, with your own token.

## Turn it off

Set `ui.enabled: false` in `control-plane.yaml`, `KBRIDGE_UI_ENABLED=false`,
or `ui.enabled=false` in the control plane chart. `/ui/` then returns 404. See
[configuration](configuration.md).

The API behind it is in the [API reference](api.md). The same tasks are
available from the CLI: see the [admin guide](admin.md).
