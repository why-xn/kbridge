# Production Install

This guide covers a hardened, multi-cluster kbridge deployment: the control plane
on a management cluster, one or more agents in target clusters, and the `kb` CLI
on operator workstations.

## 1. Prerequisites

| Requirement | Notes |
|---|---|
| Helm 3.8+ | OCI registry support (`helm pull oci://…`) |
| Two or more Kubernetes clusters | one for control plane, one per managed cluster |
| A public DNS name for control plane | e.g. `control-plane.example.com` |
| cert-manager (recommended) | or a pre-existing `kubernetes.io/tls` Secret |
| An ingress controller | the example uses ingress-nginx (`className: nginx`); with another controller change `className` and the `nginx.ingress.kubernetes.io/backend-protocol: HTTPS` annotation (§5a) to its equivalent |
| `kubectl` configured for each cluster | context switching between clusters |
| `kb` CLI | `make build` or download from releases |

## 2. Pre-create Secrets

Never pass secrets as `--set` flags in production — they appear in Helm release
history. Create the Secret first, then point the chart at it with
`auth.existingSecret`.

The chart mounts three keys from this Secret as files under
`/run/secrets/kbridge/` and passes their paths via `KBRIDGE_*_FILE` environment
variables:

| Secret key | Environment variable |
|---|---|
| `jwt_secret` | `KBRIDGE_JWT_SECRET_FILE` |
| `token_pepper` | `KBRIDGE_TOKEN_PEPPER_FILE` |
| `admin_password` | `KBRIDGE_ADMIN_PASSWORD_FILE` |

```bash
kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted
kubectl -n kbridge create secret generic kbridge-control-plane-secrets \
  --from-literal=jwt_secret="$(openssl rand -hex 32)" \
  --from-literal=token_pepper="$(openssl rand -hex 32)" \
  --from-literal=admin_password="$(openssl rand -hex 16)"
```

## 3. Install the Control Plane

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
ingress:
  enabled: true
  className: nginx
  host: control-plane.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    - secretName: kbridge-ingress-tls
      hosts: [control-plane.example.com]
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

Never upgrade this release with `--set` flags alone. Without `-f` (or
`--reuse-values`) Helm resets every other value to the chart defaults: the
Ingress disappears and `auth.existingSecret` is dropped, so the upgrade fails:
the chart requires `auth.jwtSecret` or `auth.existingSecret`.

Verify control plane is up:

```bash
curl https://control-plane.example.com/health
# {"status":"healthy","version":"v0.3.1-alpha.1"}
```

## 4. Expose gRPC :9090 to External-Cluster Agents

The HTTP Ingress above only reaches port 8080. Agents connect on gRPC port 9090,
which requires HTTP/2 end-to-end (no TLS termination at an L7 proxy).

### Option A — LoadBalancer Service (recommended)

Create a dedicated LoadBalancer Service in the management cluster. The selector
matches the control plane pod using the labels set by the Helm chart (`app.kubernetes.io/instance`
equals the Helm release name):

```yaml
apiVersion: v1
kind: Service
metadata:
  name: kbridge-control-plane-grpc
  namespace: kbridge
spec:
  type: LoadBalancer
  selector:
    app.kubernetes.io/name: kbridge-control-plane
    app.kubernetes.io/instance: kbridge-control-plane
  ports:
    - name: grpc
      port: 9090
      targetPort: 9090
      protocol: TCP
```

```bash
kubectl apply -f kbridge-control-plane-grpc-svc.yaml
# Wait for an external IP
kubectl -n kbridge get svc kbridge-control-plane-grpc -w
```

Point a DNS name, for example `kbridge-grpc.example.com`, at the external IP.
Agents set `controlPlane.url` to `kbridge-grpc.example.com:9090`; with TLS the
name must be in the control plane's certificate (step 5a).

### Option B — nginx TCP passthrough

Add the gRPC port to the nginx-ingress `tcp-services` ConfigMap. gRPC requires
HTTP/2 all the way to the backend; do **not** use an HTTP Ingress for port 9090.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: tcp-services
  namespace: ingress-nginx
data:
  "9090": "kbridge/kbridge-control-plane:9090"
```

Also patch the nginx-ingress controller Service to expose port 9090, then update
its Deployment args with `--tcp-services-configmap=ingress-nginx/tcp-services`.
Agents point `controlPlane.url` at the nginx LB host on port 9090.

## 5. TLS End-to-End

### 5a. cert-manager Certificate for control plane's gRPC listener

If agents should reach :9090 over TLS, with either option above (neither
terminates TLS), create a cert-manager Certificate and enable the chart's `tls`
stanza.

This is a second certificate. cert-manager already issues `kbridge-ingress-tls`
for the Ingress from step 3 and owns that Certificate object; a Certificate with
that name would be reverted to the Ingress host alone. The one below is for the
listener agents dial, so its `dnsNames` must include every host an agent puts
in `controlPlane.url`:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kbridge-control-plane-tls
  namespace: kbridge
spec:
  secretName: kbridge-control-plane-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - control-plane.example.com
    - kbridge-grpc.example.com
```

With option A, an HTTP-01 issuer cannot prove `kbridge-grpc.example.com`:
Let's Encrypt asks for the challenge on port 80 of that name, which points at
the gRPC LoadBalancer, not the Ingress, so the Certificate never becomes ready. Issue
this Certificate from a DNS-01 issuer. Pointing the name at the ingress
controller while it is issued also works, but every renewal needs the same.

Then merge the TLS settings into `values-control-plane.yaml` and upgrade with
the whole file. Add the top-level `tls:` block and add the annotation to the
existing `ingress:` block; do not append a second `ingress:` key, since YAML
keeps only the last one, which drops `enabled: true` and removes the Ingress. The two
blocks then read:

```yaml
tls:
  enabled: true
  secretName: kbridge-control-plane-tls
ingress:
  enabled: true
  className: nginx
  host: control-plane.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/backend-protocol: HTTPS
  tls:
    - secretName: kbridge-ingress-tls
      hosts: [control-plane.example.com]
```

```bash
helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.1-alpha.1 -f values-control-plane.yaml
```

With `tls.enabled` the pod serves HTTPS on 8080 too, so the Ingress must talk
HTTPS to it.

The chart mounts the Secret at `/etc/kbridge/tls/` and passes the paths as
`cert_file` / `key_file` in the generated config.

### 5b. Configure the agent chart for TLS

The agent chart exposes these TLS value keys under `controlPlane.tls`:

| Key | Default | Purpose |
|---|---|---|
| `controlPlane.tls.enabled` | `false` | Enable TLS for the gRPC connection |
| `controlPlane.tls.insecure` | `false` | Skip server cert verification — never set `true` in prod |
| `controlPlane.tls.caCert` | `""` | PEM-encoded CA cert; mounted as `/etc/kbridge/ca.crt` and passed as `ca_file` |

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

Store the token in a Secret in the **target** cluster:

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
  --set controlPlane.url=kbridge-grpc.example.com:9090 \
  --set controlPlane.existingSecret=kbridge-agent-secrets \
  --set controlPlane.tls.enabled=true \
  --set cluster.name=prod-us-east \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.controlPlaneTo[0].ipBlock.cidr=<EXTERNAL-IP>/32' \
  --set "networkPolicy.apiServerCIDRs={$(kubectl -n default get endpointslice kubernetes -o jsonpath='{range .endpoints[*].addresses[*]}{@}/32,{end}' | sed 's/,$//')}"
```

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
