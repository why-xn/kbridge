#!/usr/bin/env bash
# Stands up the environment docs/assets/demo.tape records against: two kind clusters
# running the published charts (kb-demo holds the control plane and the
# "staging" agent, kb-demo-prod the "production" agent), an admin login, a
# developer user, the policy from the guide and a workload. Then `make demo`
# renders the GIF. Everything lives under DEMO_HOME (default ~/kb-demo);
# nothing touches ~/.kbridge or your kubeconfig.
# Tear down: kind delete cluster --name kb-demo; kind delete cluster --name kb-demo-prod
set -euo pipefail
W="${DEMO_HOME:-$HOME/kb-demo}"
mkdir -p "$W/bin" "$W/admin" "$W/docker"
export DOCKER_CONFIG="$W/docker" HELM_REGISTRY_CONFIG="$W/docker/config.json"
echo '{}' > "$HELM_REGISTRY_CONFIG"
export KUBECONFIG="$W/kubeconfig"
CLUSTER=staging

echo "== CLI"
[ -x "$W/bin/kb" ] || KBRIDGE_VERSION=v0.3.0-alpha.1 KBRIDGE_INSTALL_DIR="$W/bin" sh -c \
  "$(curl -fsSL https://raw.githubusercontent.com/why-xn/kbridge/master/install.sh)"
kb() { HOME="$W/admin" "$W/bin/kb" "$@"; }

echo "== kind"
cat > "$W/kind.yaml" <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: kb-demo
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 8080
      - containerPort: 30090
        hostPort: 9090
EOF
kind get clusters | grep -qx kb-demo || kind create cluster --config "$W/kind.yaml"

echo "== control plane"
kubectl create namespace kbridge --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace kbridge pod-security.kubernetes.io/enforce=restricted --overwrite
ADMIN_PASSWORD="$(openssl rand -hex 12)"; echo "$ADMIN_PASSWORD" > "$W/admin-password"
helm upgrade --install kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.0-alpha.1 \
  --set auth.jwtSecret="$(openssl rand -hex 32)" --set auth.adminPassword="$ADMIN_PASSWORD"
kubectl -n kbridge rollout status deploy/kbridge-control-plane --timeout=180s
cat <<'EOF' | kubectl apply -f -
apiVersion: v1
kind: Service
metadata: {name: kbridge-control-plane-nodeport, namespace: kbridge}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: kbridge-control-plane, app.kubernetes.io/instance: kbridge-control-plane}
  ports:
    - {name: http, port: 8080, targetPort: 8080, nodePort: 30080}
    - {name: grpc, port: 9090, targetPort: 9090, nodePort: 30090}
EOF
for _ in $(seq 1 30); do curl -fs localhost:8080/health >/dev/null && break; sleep 2; done
curl -s localhost:8080/health; echo

echo "== admin login"
mkdir -p "$W/admin/.kbridge"
printf 'control_plane_url: http://localhost:8080\n' > "$W/admin/.kbridge/config.yaml"
printf 'admin@kbridge.local\n%s\n' "$ADMIN_PASSWORD" | kb login
kb status

echo "== agent"
TOKEN=$(kb admin agent-tokens create --cluster "$CLUSTER" --description "demo" | grep -o 'kbat_[A-Za-z0-9_-]*')
helm upgrade --install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  -n kbridge --version 0.3.0-alpha.1 \
  --set controlPlane.url=kbridge-control-plane.kbridge.svc:9090 \
  --set controlPlane.token="$TOKEN" --set cluster.name="$CLUSTER"
kubectl -n kbridge rollout status deploy/kbridge-agent --timeout=180s

echo "== second cluster"
# Its agent reaches the control plane through the first cluster's node on the
# shared kind docker network.
kind get clusters | grep -qx kb-demo-prod || kind create cluster --name kb-demo-prod
NODE_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' kb-demo-control-plane)
PROD=(--context kind-kb-demo-prod)
kubectl "${PROD[@]}" create namespace kbridge --dry-run=client -o yaml | kubectl "${PROD[@]}" apply -f -
kubectl "${PROD[@]}" label namespace kbridge pod-security.kubernetes.io/enforce=restricted --overwrite
TOKEN=$(kb admin agent-tokens create --cluster production --description "demo" | grep -o 'kbat_[A-Za-z0-9_-]*')
helm upgrade --install kbridge-agent oci://ghcr.io/why-xn/charts/kbridge-agent \
  --kube-context kind-kb-demo-prod -n kbridge --version 0.3.0-alpha.1 \
  --set controlPlane.url="$NODE_IP:30090" \
  --set controlPlane.token="$TOKEN" --set cluster.name=production
kubectl "${PROD[@]}" -n kbridge rollout status deploy/kbridge-agent --timeout=180s
kubectl "${PROD[@]}" create namespace team-a --dry-run=client -o yaml | kubectl "${PROD[@]}" apply -f -
kubectl "${PROD[@]}" label ns team-a pod-security.kubernetes.io/enforce=baseline --overwrite
kubectl config use-context kind-kb-demo

echo "== users, namespace, policy"
kb admin users create --email dev@example.com --name "Dev" --password dev-password-123 || true
kubectl create namespace team-a --dry-run=client -o yaml | kubectl apply -f -
kubectl label ns team-a pod-security.kubernetes.io/enforce=baseline --overwrite
cat > "$W/rbac.yaml" <<'EOF'
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
      - clusters: ["*"]
        namespaces: ["team-a"]
        resources: ["*"]
        verbs: ["*"]
    clusterAccess:
      - clusters: ["*"]
        clusterRole: edit
        namespaces: ["team-a"]
bindings:
  - subject: admin@kbridge.local
    roles: ["admin"]
  - subject: dev@example.com
    roles: ["developer"]
EOF
helm upgrade kbridge-control-plane oci://ghcr.io/why-xn/charts/kbridge-control-plane \
  -n kbridge --version 0.3.0-alpha.1 --reuse-values --set-file rbac.policy="$W/rbac.yaml"
kubectl -n kbridge rollout status deploy/kbridge-control-plane --timeout=180s
for _ in $(seq 1 30); do curl -fs localhost:8080/health >/dev/null && break; sleep 2; done

echo "== workload"
for _ in $(seq 1 30); do [ "$(kb clusters list 2>/dev/null | grep -cw connected)" -ge 2 ] && break; sleep 3; done
kb clusters use "$CLUSTER"
kb create deployment web --image=nginx:1.27-alpine --replicas=2 -n team-a || true
kb rollout status deploy/web -n team-a --timeout=180s
echo "== wait for rbac sync"
for _ in $(seq 1 40); do [ "$(kb admin rbac status | grep -c "in sync")" -ge 2 ] && break; sleep 5; done
kb admin rbac status
kb get pods -n team-a
echo "== READY"
