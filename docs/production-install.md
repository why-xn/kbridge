# Production Install

This guide covers a hardened, multi-cluster kbridge deployment: the control plane
on a management cluster, one or more agents in target clusters, and the `kb` CLI
on operator workstations.

## What you will have at the end

```text
users: kb, browser ─── HTTPS ─── control-plane.example.com:443  ────┐
                                                                    ├── LoadBalancer Service ── control plane
agents in target clusters ─ gRPC ─ control-plane.example.com:9090 ──┘
```

The control plane runs in the management cluster, in the namespace `kbridge`,
and one `LoadBalancer` Service there carries both ports. It serves TLS itself
on both with one certificate (from cert-manager in this guide) and reads its
secrets from a Kubernetes Secret. Each target cluster runs an agent that dials
out to port 9090; nothing dials into a target cluster.

### Placeholders to replace

The commands below use these example values. Replace each with yours
everywhere it appears:

| Placeholder | Replace with | Used in |
|---|---|---|
| `control-plane.example.com` | The DNS name users and agents reach the control plane at | §3a `dnsNames`; §3c DNS record and health check; §4 and §7 `controlPlane.url`; §6 `kb login` |
| `letsencrypt-dns` | Your cert-manager `ClusterIssuer` (DNS-01 or private CA) | §3a |
| `admin@example.com` | The email of the admin account the chart seeds | §3b `auth.adminEmail` and the policy's binding; §6 `kb login` |
| `<EXTERNAL-IP>` | The external IP of the §3c Service | §7 `networkPolicy.controlPlaneTo`; [Moving from an HTTP Ingress](#moving-from-an-http-ingress) step 5 |
| `prod-us-east` | The target cluster's name in kbridge (here also its kubectl context) | §6 `--cluster` and `kubectl config use-context`; §7 `cluster.name`; §8 |
| `management` | The management cluster's kubectl context | §8 |
| `ca.crt`, `/path/to/ca.crt` | Your private CA's certificate; not needed with a public CA | §3c health check; §5b; §6 `SSL_CERT_FILE`; §7 `controlPlane.tls.caCert` |
| `kbat_xxxxxxxxxxxxxxxxxxxx` | The agent token §6 prints | §6 the agent's Secret |

The namespace `kbridge`, the release names and the Secret names
(`kbridge-control-plane-secrets`, `kbridge-control-plane-tls`,
`kbridge-agent-secrets`) can stay as they are; if you change one, change it
in every command.

## 1. Prerequisites

| Requirement | Notes |
|---|---|
| Helm 3.8+ | OCI registry support (`helm pull oci://…`) |
| Two or more Kubernetes clusters | one for control plane, one per managed cluster |
| A DNS name for control plane | e.g. `control-plane.example.com`; users and agents both use it |
| `LoadBalancer` Services in the management cluster | users and agents reach the control plane through one L4 load balancer (§3). An HTTP Ingress does not work for this; see [Why not an HTTP Ingress](#why-not-an-http-ingress) |
| cert-manager (recommended) | with a DNS-01 or private CA issuer (§3a), or a pre-existing `kubernetes.io/tls` Secret |
| `kubectl` configured for each cluster | context switching between clusters |
| `kb` CLI | `make build` or download from releases |

## 2. Pre-create Secrets

Never pass secrets as `--set` flags in production — they appear in Helm release
history. Create the Secret first, then point the chart at it with
`auth.existingSecret`.

The Secret holds three keys, `jwt_secret`, `token_pepper` and `admin_password`.
The chart mounts them as files under `/run/secrets/kbridge/` and passes their
paths in `KBRIDGE_JWT_SECRET_FILE`, `KBRIDGE_TOKEN_PEPPER_FILE` and
`KBRIDGE_ADMIN_PASSWORD_FILE` (see [configuration.md](configuration.md#control-plane-control-planeyaml)):

```bash
kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted
kubectl -n kbridge create secret generic kbridge-control-plane-secrets \
  --from-literal=jwt_secret="$(openssl rand -hex 32)" \
  --from-literal=token_pepper="$(openssl rand -hex 32)" \
  --from-literal=admin_password="$(openssl rand -hex 16)"
```

## 3. Install the Control Plane

The control plane serves TLS itself, and one `LoadBalancer` Service carries
both of its ports: HTTPS for users and the console, gRPC on 9090 for agents.

### Why not an HTTP Ingress

Do not put an HTTP (L7) Ingress in front of the control plane. `kb exec -i`,
`kb exec -it` and `kb port-forward` send and receive on one HTTP/2 request at
the same time; an L7 proxy such as ingress-nginx does not pass that through,
so they never connect. `kb logs -f` and `-w` connect, but the proxy cuts them
when no output arrives for its idle timeout (60 s by default in ingress-nginx).
`kb login` and one-shot commands work, which hides the problem until a user
opens a shell. Raising the proxy's timeouts fixes only `logs -f`.

If you already run ingress-nginx and want the control plane behind it, use TLS
passthrough instead ([note below](#behind-ingress-nginx-tls-passthrough)).

### 3a. Issue the control plane's certificate

Its `dnsNames` must include every host users put in `kb login` and agents put
in `controlPlane.url`; add `kbridge-control-plane.kbridge.svc` too if an agent
runs in this cluster:

```yaml
# kbridge-control-plane-tls.yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kbridge-control-plane-tls
  namespace: kbridge
spec:
  secretName: kbridge-control-plane-tls
  issuerRef:
    name: letsencrypt-dns
    kind: ClusterIssuer
  dnsNames:
    - control-plane.example.com
```

```bash
kubectl apply -f kbridge-control-plane-tls.yaml
kubectl -n kbridge wait --for=condition=Ready certificate/kbridge-control-plane-tls --timeout=5m
```

Use a DNS-01 issuer (here `letsencrypt-dns`) or a private CA issuer. An
HTTP-01 issuer cannot prove the name: Let's Encrypt asks for the challenge on
port 80 of `control-plane.example.com`, which points at the load balancer from
§3c, not at an ingress controller, so the Certificate never becomes ready.

### 3b. Install the chart

`auth.adminEmail` only names the account the chart seeds. What that account may
do in clusters comes from the policy's `bindings`, and the chart's default
policy binds `admin@kbridge.local`. Keep the two in step with a values file
that carries both, and keep the file: every later `helm upgrade` passes it
again.

```yaml
# values-control-plane.yaml
auth:
  existingSecret: kbridge-control-plane-secrets
  adminEmail: admin@example.com
persistence:
  enabled: true
tls:
  enabled: true
  secretName: kbridge-control-plane-tls
rbac:
  policy: |
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
      - name: viewer
        rules:
          - clusters: ["*"]
            namespaces: ["*"]
            resources: ["*"]
            verbs: ["get", "list", "watch", "describe", "logs", "auth"]
        clusterAccess:
          - clusters: ["*"]
            clusterRole: view
            clusterWide: true
    bindings:
      - subject: admin@example.com
        roles: ["admin"]
```

```bash
helm install kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.1-alpha.1 -f values-control-plane.yaml
```

With `tls.enabled` the pod serves HTTPS on 8080 and TLS on 9090 with the same
certificate. The chart mounts the Secret at `/etc/kbridge/tls/` and passes the
paths as `cert_file` / `key_file` in the generated config.

Never upgrade this release with `--set` flags alone, or with `--reuse-values`
(it fails when the chart version changes; see
[Helm upgrade](operations.md#helm-upgrade)). Without
`-f values-control-plane.yaml` Helm resets every other value to the chart
defaults: TLS is turned off and `auth.existingSecret` is dropped, so the upgrade fails: the
chart requires `auth.jwtSecret` or `auth.existingSecret`.

### 3c. Expose it with a LoadBalancer Service

The chart's own Service stays `ClusterIP`. Create a `LoadBalancer` Service
that sends 443 to the HTTPS port and 9090 to gRPC. Its selector matches the
control plane pod by the chart's labels (`app.kubernetes.io/instance` equals
the Helm release name):

```yaml
# kbridge-control-plane-lb.yaml
apiVersion: v1
kind: Service
metadata:
  name: kbridge-control-plane-lb
  namespace: kbridge
spec:
  type: LoadBalancer
  selector:
    app.kubernetes.io/name: kbridge-control-plane
    app.kubernetes.io/instance: kbridge-control-plane
  ports:
    - name: https
      port: 443
      targetPort: 8080
      protocol: TCP
    - name: grpc
      port: 9090
      targetPort: 9090
      protocol: TCP
```

```bash
kubectl apply -f kbridge-control-plane-lb.yaml
# Wait for an external IP
kubectl -n kbridge get svc kbridge-control-plane-lb -w
```

Point `control-plane.example.com` at the external IP. Users run
`kb login` against `https://control-plane.example.com`, the console is at
`https://control-plane.example.com/`, and agents set `controlPlane.url` to
`control-plane.example.com:9090`.

Verify control plane is up:

```bash
curl https://control-plane.example.com/health
# {"status":"healthy","version":"v0.3.1-alpha.1"}
```

With a private CA, curl needs the CA: add `--cacert ca.crt`. A cert-manager
CA issuer puts its CA in every Secret it issues:

```bash
kubectl -n kbridge get secret kbridge-control-plane-tls -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
curl --cacert ca.crt https://control-plane.example.com/health
```

### Moving from an HTTP Ingress

If you installed from an earlier version of this guide, with an Ingress that
ends TLS:

1. **Issue the control plane's own certificate** for every host name users
   and agents dial (§3a). If the old Ingress has a
   `cert-manager.io/cluster-issuer` annotation, cert-manager owns
   `kbridge-control-plane-tls` through it: it undoes §3a and deletes the
   Certificate together with the Ingress, so the Secret is never renewed.
   Detach it first:

   ```bash
   kubectl -n kbridge patch certificate kbridge-control-plane-tls --type=json \
     -p '[{"op":"remove","path":"/metadata/ownerReferences"}]'
   kubectl apply -f kbridge-control-plane-tls.yaml
   ```

2. **Write `values-control-plane.yaml` as in §3b** (the earlier guide used
   `--set` flags, so there may be no file yet). Leave out `ingress:` and
   create §3c's Service, or add the passthrough `ingress:` block (below).
3. **Upgrade with the file and without `--reuse-values`**, so the old Ingress
   values are dropped, then point DNS at the new address:

   ```bash
   helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
     -n kbridge --version 0.3.1-alpha.1 -f values-control-plane.yaml
   ```

4. **Users** keep their `https://` URL. If the certificate's CA changed, they
   need the new CA (`SSL_CERT_FILE`).
5. **Agents**: the control plane now serves TLS on 9090 with this
   certificate. Upgrade each agent with TLS on and the CA. If it moves to a
   new address, also change its URL and its NetworkPolicy peer:

   ```bash
   helm get values kbridge-agent -n kbridge -o yaml > agent-values.yaml
   helm upgrade kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
     -n kbridge --version 0.3.1-alpha.1 -f agent-values.yaml \
     --set controlPlane.url=control-plane.example.com:9090 \
     --set controlPlane.tls.enabled=true --set controlPlane.tls.caCert="$(cat ca.crt)" \
     --set 'networkPolicy.controlPlaneTo[0].ipBlock.cidr=<EXTERNAL-IP>/32'
   rm agent-values.yaml
   ```

   The saved values keep the rest of the agent's install (not
   `--reuse-values`: see [Helm upgrade](operations.md#helm-upgrade)).

### Behind ingress-nginx: TLS passthrough

ingress-nginx is one controller that can hand the TLS connection to the
control plane untouched, which keeps every stream working. Start the
controller with `--enable-ssl-passthrough` (with its Helm chart:
`--set controller.extraArgs.enable-ssl-passthrough=true`), then add an
`ingress:` block to `values-control-plane.yaml` and upgrade with the file:

```yaml
ingress:
  enabled: true
  className: nginx
  host: control-plane.example.com
  annotations:
    nginx.ingress.kubernetes.io/ssl-passthrough: "true"
```

The control plane presents `kbridge-control-plane-tls` from §3a, so the
Ingress needs no `tls:` list and no cert-manager annotation. Point
`control-plane.example.com` at the ingress controller. Passthrough carries
only HTTPS: agents still need 9090, either through the controller's
`tcp-services` ConfigMap (§4) or through §3c's Service without its `https`
port, under a second name that is also in the certificate's `dnsNames`.

Every ingress-nginx reload (any Ingress change in the cluster) closes the
connections through it after its `worker-shutdown-timeout` (240 s by default):
open shells end and agents reconnect.

## 4. Agents in Other Clusters

Agents dial gRPC on port 9090 and need HTTP/2 end to end, so never route 9090
through an HTTP Ingress. §3c already exposes it: agents set
`controlPlane.url` to `control-plane.example.com:9090`.

With the ingress-nginx passthrough setup instead, the controller can carry
9090 as plain TCP. If ingress-nginx is installed from its Helm chart, one
value adds the ConfigMap entry, the Service port and the controller argument
(hand edits would be undone by its next upgrade):

```bash
helm upgrade ingress-nginx ingress-nginx --repo https://kubernetes.github.io/ingress-nginx \
  -n ingress-nginx --version <chart-version> --reuse-values \
  --set tcp.9090=kbridge/kbridge-control-plane:9090
```

`<chart-version>` is the version you run, the `CHART` column of
`helm list -n ingress-nginx` without `ingress-nginx-`. Without `--version`
Helm moves to the newest chart while keeping the old one's defaults.

Without the chart, add the port to its `tcp-services` ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: tcp-services
  namespace: ingress-nginx
data:
  "9090": "kbridge/kbridge-control-plane:9090"
```

then expose port 9090 on the controller's Service and add
`--tcp-services-configmap=ingress-nginx/tcp-services` to the controller's
arguments:

```bash
kubectl -n ingress-nginx patch svc ingress-nginx-controller --type=json \
  -p '[{"op":"add","path":"/spec/ports/-","value":{"name":"grpc","port":9090,"targetPort":9090}}]'
kubectl -n ingress-nginx patch deploy ingress-nginx-controller --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--tcp-services-configmap=ingress-nginx/tcp-services"}]'
```

Agents point `controlPlane.url` at the nginx LB host on port 9090, which must
be one of the certificate's `dnsNames`.

## 5. TLS End-to-End

### 5a. The control plane's certificate

§3 already serves HTTPS and gRPC with `kbridge-control-plane-tls`. To add a
host later, add it to the Certificate's `dnsNames` and apply it; cert-manager
reissues the Secret.

The control plane reads the certificate only when it starts, so restart it
after cert-manager reissues or renews the Secret; see
[Certificate renewal](operations.md#certificate-renewal).

### 5b. Configure the agent chart for TLS

The agent's TLS values are `controlPlane.tls.enabled`, `controlPlane.tls.caCert`
and `controlPlane.tls.insecure`; [configuration.md](configuration.md#agent-agentyaml)
lists their defaults.

For a public cert (Let's Encrypt / DigiCert) the system CA bundle is sufficient;
set `controlPlane.tls.enabled=true` and leave `caCert` empty:

```bash
--set controlPlane.tls.enabled=true
```

For a private CA, embed the PEM in the value:

```bash
--set controlPlane.tls.enabled=true \
--set controlPlane.tls.caCert="$(cat /path/to/ca.crt)"
```

Never set `controlPlane.tls.insecure=true` in production — it disables certificate
verification and exposes agent tokens to interception.

## 6. Bootstrap: Create an Agent Token

Log in as the admin user that was seeded from the `admin_password` secret:

```bash
kb login
# Control plane URL: https://control-plane.example.com
# Email: admin@example.com
# Password: <value of admin_password key from kbridge-control-plane-secrets>
```

Read the seeded password with
`kubectl -n kbridge get secret kbridge-control-plane-secrets -o jsonpath='{.data.admin_password}' | base64 -d`.
With a private CA (not Let's Encrypt), the CLI needs it too: run
`export SSL_CERT_FILE=/path/to/ca.crt` before `kb login`, and give the same
file to your users (see
[security.md](security.md#4-trust-the-control-planes-ca-on-the-cli-keep-insecure_skip_verify-off)).

Create a token for the first target cluster:

```bash
kb admin agent-tokens create --cluster prod-us-east
# Agent token for cluster "prod-us-east" created.
#
#   kbat_xxxxxxxxxxxxxxxxxxxx
#
# Store it now — it cannot be retrieved again. ...
```

Ignore any hint to set `controlPlane.token` directly: in production the token
goes in a Secret. Store it in the **target** cluster:

```bash
# Switch context to the target cluster
kubectl config use-context prod-us-east

kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted

kubectl create secret generic kbridge-agent-secrets -n kbridge \
  --from-literal=token="kbat_xxxxxxxxxxxxxxxxxxxx"
```

The agent chart expects the Secret to contain a key named `token` and mounts it
at `/etc/kbridge-token/token`, passing it via `KBRIDGE_AGENT_TOKEN_FILE`.

## 7. Install the Agent Chart in the Target Cluster

```bash
# Still in the target cluster context
helm install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.1-alpha.1 \
  --set controlPlane.url=control-plane.example.com:9090 \
  --set controlPlane.existingSecret=kbridge-agent-secrets \
  --set controlPlane.tls.enabled=true \
  --set cluster.name=prod-us-east \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.controlPlaneTo[0].ipBlock.cidr=<EXTERNAL-IP>/32' \
  --set "networkPolicy.apiServerCIDRs={$(kubectl -n default get endpointslice kubernetes -o jsonpath='{range .endpoints[*].addresses[*]}{@}/32,{end}' | sed 's/,$//')}"
```

`<EXTERNAL-IP>` is the external IP of the §3c Service (with the ingress-nginx
passthrough setup: of the Service agents reach on 9090, the controller's with
`tcp-services`, or §3c's). If the Service shows a host name instead of an IP,
as some clouds do, use each address behind it (`dig +short <hostname>`) as its
own peer (`--set 'networkPolicy.controlPlaneTo[1].ipBlock.cidr=<second-IP>/32'`),
or leave `networkPolicy` off where those addresses can change.

The agent's ServiceAccount token is not mounted at the default path; the chart
projects it with the cluster CA and namespace at `/var/run/kbridge/sa`
(`defaultMode: 0400`, readable only by the agent user through `fsGroup: 10001`,
rotated every `serviceAccountToken.expirationSeconds`, default 600). kubectl
itself runs with no credential and reaches the cluster only through the agent.

The NetworkPolicy limits the agent's egress to the API server
(`networkPolicy.apiServerCIDRs` on `networkPolicy.apiServerPorts`, default
443 and 6443), the control plane (`networkPolicy.controlPlaneTo`, a
NetworkPolicyPeer list, on `networkPolicy.controlPlanePort`, default 9090) and
`kube-dns` in `kube-system`. It is off by default; when enabled, both lists are
required and the render fails without them. It needs a CNI that enforces
NetworkPolicy.

The release namespace is always protected: users cannot run commands in it
through kbridge. If the control plane also runs in this cluster, install the
agent in the control plane's namespace (`kbridge`), or protect that namespace
too: `--set 'protectedNamespaces={<control-plane-namespace>}'`.

Label each namespace where kbridge will grant `edit`:
`kubectl label ns <ns> pod-security.kubernetes.io/enforce=baseline`. Without
it the agent refuses the binding and `kb admin rbac status` shows the fix.

For a private CA add `--set controlPlane.tls.caCert="$(cat ca.crt)"`.

Every command runs as `kbridge:<email>`, and Kubernetes RBAC bound to that
user decides what it may do. The agent creates those bindings from the
policy's `clusterAccess`, up to the chart's `accessCeiling` (default: `view`,
`edit` and `cluster-admin`, ClusterRoleBindings allowed; the default policy maps
`admin` to `cluster-admin` and `viewer` to `view`). To limit what any kbridge
policy can grant in a cluster, lower the ceiling, for example
`--set 'accessCeiling.clusterRoles={view,edit}' --set accessCeiling.clusterWide=false`,
and check `kb admin rbac status` after install (see
[security.md](security.md#binding-kbridge-users-in-the-cluster)).

## 8. Verify the Deployment

```bash
# Switch back to the management cluster context
kubectl config use-context management

# Check control plane pod logs
kubectl -n kbridge logs deployment/kbridge-control-plane

# From the CLI
kb clusters list
# CURRENT   NAME           STATUS
#           prod-us-east   connected

kb clusters use prod-us-east
kb get nodes
```

The `connected` status confirms the agent has registered and its heartbeat is
current. If the status is `disconnected`, check:

1. Agent pod logs: `kubectl -n kbridge logs deployment/kbridge-agent` (in target cluster).
2. `controlPlane.url` — must be reachable from inside the target cluster on port 9090.
3. TLS — if `controlPlane.tls.insecure` was not set and the cert is self-signed,
   provide the CA via `controlPlane.tls.caCert`.
4. Token — `KBRIDGE_AGENT_TOKEN_FILE` must resolve to a valid, non-revoked token.
5. Certificate name — the host in `controlPlane.url` must be one of the control
   plane certificate's names. A mismatch logs only `dialing control plane:
   context deadline exceeded`; check with
   `openssl s_client -connect <host>:9090 -servername <host> -verify_hostname <host>`.
6. NetworkPolicy — with `networkPolicy.enabled=true`, `controlPlaneTo` must
   contain the address the agent dials and `controlPlanePort` the port. The
   failure looks the same as 5; upgrade once with
   `--set networkPolicy.enabled=false` to rule it out.

## Next

1. [Security hardening](security.md): the checklist before users arrive.
2. [Onboard a user](admin.md#onboard-a-user): the first users, their roles and
   their first password.
3. [Operations](operations.md): backup, upgrades, troubleshooting.
