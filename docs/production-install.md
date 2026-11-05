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
kubectl create secret generic kbridge-control-plane-secrets \
  --from-literal=jwt_secret="$(openssl rand -hex 32)" \
  --from-literal=token_pepper="$(openssl rand -hex 32)" \
  --from-literal=admin_password="$(openssl rand -hex 16)"
```

## 3. Install the Control Plane

```bash
helm install kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  --version 0.2.2-alpha.1 \
  --set auth.existingSecret=kbridge-control-plane-secrets \
  --set auth.adminEmail=admin@example.com \
  --set persistence.enabled=true \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.host=control-plane.example.com \
  --set 'ingress.annotations.cert-manager\.io/cluster-issuer=letsencrypt-prod' \
  --set 'ingress.tls[0].secretName=kbridge-control-plane-tls' \
  --set 'ingress.tls[0].hosts[0]=control-plane.example.com'
```

Verify control plane is up:

```bash
curl https://control-plane.example.com/health
# {"status":"healthy"}
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
  namespace: default
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
kubectl get svc kbridge-control-plane-grpc -w
```

Agents will point `controlPlane.url` at `<EXTERNAL-IP>:9090`.

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
  "9090": "default/kbridge-control-plane:9090"
```

Also patch the nginx-ingress controller Service to expose port 9090, then update
its Deployment args with `--tcp-services-configmap=ingress-nginx/tcp-services`.
Agents point `controlPlane.url` at the nginx LB host on port 9090.

## 5. TLS End-to-End

### 5a. cert-manager Certificate for control plane's gRPC listener

If you want the control plane pod itself to terminate TLS on :9090 (needed when using
option B above, or for mTLS), create a cert-manager Certificate and enable the
chart's `tls` stanza:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: kbridge-control-plane-tls
  namespace: default
spec:
  secretName: kbridge-control-plane-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  dnsNames:
    - control-plane.example.com
```

Then add to your Helm install (or `helm upgrade`):

```bash
--set tls.enabled=true \
--set tls.secretName=kbridge-control-plane-tls
```

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
kb login --server https://control-plane.example.com
# Username: admin@example.com
# Password: <value of admin_password key from kbridge-control-plane-secrets>
```

Create a token for the first target cluster:

```bash
kb admin agent-tokens create --cluster prod-us-east
# Agent token: kba_xxxxxxxxxxxxxxxxxxxx
```

Store the token in a Secret in the **target** cluster:

```bash
# Switch context to the target cluster
kubectl config use-context prod-us-east

kubectl create namespace kbridge
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted

kubectl create secret generic kbridge-agent-secrets -n kbridge \
  --from-literal=token="kba_xxxxxxxxxxxxxxxxxxxx"
```

The agent chart expects the Secret to contain a key named `token` and mounts it
at `/etc/kbridge-token/token`, passing it via `KBRIDGE_AGENT_TOKEN_FILE`.

## 7. Install the Agent Chart in the Target Cluster

```bash
# Still in the target cluster context
helm install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent -n kbridge \
  --version 0.2.2-alpha.1 \
  --set controlPlane.url=<EXTERNAL-IP>:9090 \
  --set controlPlane.existingSecret=kbridge-agent-secrets \
  --set controlPlane.tls.enabled=true \
  --set cluster.name=prod-us-east \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.controlPlaneTo[0].ipBlock.cidr=<EXTERNAL-IP>/32' \
  --set "networkPolicy.apiServerCIDRs={$(kubectl get endpoints kubernetes -o jsonpath='{range .subsets[*].addresses[*]}{.ip}/32,{end}' | sed 's/,$//')}"
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
through kbridge. If the control plane also runs in this cluster, protect its
namespace too: `--set 'protectedNamespaces={kbridge-system}'`.

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
kubectl logs deployment/kbridge-control-plane

# From the CLI
kb clusters list
# NAME            STATUS     VERSION
# prod-us-east    connected  v1.30.0

kb get nodes --cluster prod-us-east
```

The `connected` status confirms the agent has registered and its heartbeat is
current. If the status is `disconnected`, check:

1. Agent pod logs: `kubectl logs deployment/kbridge-agent-agent` (in target cluster).
2. `controlPlane.url` — must be reachable from inside the target cluster on port 9090.
3. TLS — if `controlPlane.tls.insecure` was not set and the cert is self-signed,
   provide the CA via `controlPlane.tls.caCert`.
4. Token — `KBRIDGE_AGENT_TOKEN_FILE` must resolve to a valid, non-revoked token.
