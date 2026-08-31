#!/usr/bin/env bash
#
# Bring up the full platform on a local kind cluster.
#
# This exists so the Kubernetes layer of this project can be demonstrated by
# anyone, on a laptop, for free — rather than only by someone willing to pay for
# an EKS control plane. The same charts, the same policies and the same probes
# apply here as in AWS; only the Terraform layer is AWS-specific.
set -euo pipefail

CLUSTER_NAME="fleet-platform"
NAMESPACE="fleet-platform"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="local-$(date +%s)"

info()  { printf '\033[0;34m==>\033[0m %s\n' "$*"; }
ok()    { printf '\033[0;32m  ok\033[0m %s\n' "$*"; }
die()   { printf '\033[0;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

for tool in kind kubectl helm docker; do
  command -v "$tool" >/dev/null || die "$tool is required but not installed"
done
docker info >/dev/null 2>&1 || die "the Docker daemon is not reachable"

# --- Cluster -----------------------------------------------------------------

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  info "Reusing the existing '$CLUSTER_NAME' cluster"
else
  info "Creating the kind cluster (3 workers with distinct zone labels)"
  kind create cluster --config "$REPO_ROOT/local/kind-config.yaml" --wait 120s
fi

kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null

# --- CNI ---------------------------------------------------------------------

# kind's default CNI ignores NetworkPolicy entirely. Without a policy-enforcing
# CNI the default-deny policies these charts render would apply cleanly and
# enforce nothing, which gives a false sense of security locally.
if ! kubectl get daemonset -n kube-system calico-node >/dev/null 2>&1; then
  info "Installing Calico so NetworkPolicy is actually enforced"
  kubectl apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/calico.yaml
  kubectl -n kube-system rollout status daemonset/calico-node --timeout=300s
fi
kubectl wait --for=condition=Ready nodes --all --timeout=300s
ok "cluster ready"

# --- metrics-server ----------------------------------------------------------

# The HPA reads from the metrics API. Without metrics-server it reports
# "unknown" for every metric and never scales — the single most common reason a
# working HPA config appears to do nothing.
if ! kubectl get deployment -n kube-system metrics-server >/dev/null 2>&1; then
  info "Installing metrics-server (the HPA has nothing to read without it)"
  kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml
  # kind nodes serve their kubelet certificates with an IP SAN that
  # metrics-server does not trust, so it fails to scrape until told not to
  # verify. Acceptable locally; never in a real cluster.
  kubectl -n kube-system patch deployment metrics-server --type=json \
    -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
  kubectl -n kube-system rollout status deployment/metrics-server --timeout=180s
fi
ok "metrics-server ready"

# --- Images ------------------------------------------------------------------

info "Building images"
docker build -q -t "fleet/gateway:$IMAGE_TAG" \
  --build-arg SERVICE=gateway --build-arg VERSION="$IMAGE_TAG" \
  -f "$REPO_ROOT/build/go.Dockerfile" "$REPO_ROOT"
docker build -q -t "fleet/catalog:$IMAGE_TAG" \
  --build-arg SERVICE=catalog --build-arg VERSION="$IMAGE_TAG" \
  -f "$REPO_ROOT/build/go.Dockerfile" "$REPO_ROOT"
docker build -q -t "fleet/scorer:$IMAGE_TAG" \
  -f "$REPO_ROOT/build/scorer.Dockerfile" "$REPO_ROOT"

# Load into the nodes' image stores. Without this the pods sit in
# ImagePullBackOff trying to reach a registry that has never heard of these tags.
info "Loading images into the cluster"
for svc in gateway catalog scorer; do
  kind load docker-image "fleet/$svc:$IMAGE_TAG" --name "$CLUSTER_NAME" >/dev/null
done
ok "images loaded"

# --- Dependencies ------------------------------------------------------------

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# The monitoring namespace must carry the label the NetworkPolicy selects on.
# Without it the policy blocks Prometheus and every target goes down.
kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -
kubectl label namespace monitoring kubernetes.io/metadata.name=monitoring --overwrite >/dev/null

info "Installing Postgres for the catalog"
helm repo add bitnami https://charts.bitnami.com/bitnami >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install postgres bitnami/postgresql \
  --namespace "$NAMESPACE" \
  --set auth.username=platform \
  --set auth.password=platform \
  --set auth.database=catalog \
  --set primary.persistence.enabled=false \
  --wait --timeout 5m

# In a real environment this Secret is created by External Secrets from AWS
# Secrets Manager. Locally there is no Secrets Manager, so it is created
# directly — the one place local deliberately diverges from deployed.
kubectl -n "$NAMESPACE" create secret generic catalog-secrets \
  --from-literal=database-url="postgres://platform:platform@postgres-postgresql:5432/catalog?sslmode=disable" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "$NAMESPACE" create secret generic gateway-secrets \
  --from-literal=api-keys="local-dev:local-development-key" \
  --dry-run=client -o yaml | kubectl apply -f -

# --- Platform ----------------------------------------------------------------

info "Deploying the platform charts"
for svc in catalog scorer gateway; do
  helm dependency update "$REPO_ROOT/deploy/charts/$svc" >/dev/null
  helm upgrade --install "$svc" "$REPO_ROOT/deploy/charts/$svc" \
    --namespace "$NAMESPACE" \
    -f "$REPO_ROOT/deploy/env/dev.yaml" \
    --set image.repository="fleet/$svc" \
    --set image.tag="$IMAGE_TAG" \
    --set image.pullPolicy=Never \
    --set metrics.podMonitor.enabled=false \
    --wait --timeout 5m
  ok "$svc deployed"
done

# The scorer's HPA is the point of the exercise, so turn it on here even though
# the dev overlay leaves autoscaling minimal.
helm upgrade --install scorer "$REPO_ROOT/deploy/charts/scorer" \
  --namespace "$NAMESPACE" \
  -f "$REPO_ROOT/deploy/env/dev.yaml" \
  --set image.repository="fleet/scorer" \
  --set image.tag="$IMAGE_TAG" \
  --set image.pullPolicy=Never \
  --set metrics.podMonitor.enabled=false \
  --set autoscaling.enabled=true \
  --set autoscaling.minReplicas=1 \
  --set autoscaling.maxReplicas=8 \
  --set podDisruptionBudget.enabled=false \
  --wait --timeout 5m

cat <<EOF

Platform is up on kind.

  kubectl -n $NAMESPACE get pods
  kubectl -n $NAMESPACE port-forward svc/gateway 8080:8080

Register a device and score a reading:

  curl -s localhost:8080/api/v1/devices \\
    -H 'content-type: application/json' \\
    -d '{"id":"pump-01","name":"West Pump","site":"west"}'

  curl -s localhost:8080/api/v1/readings/score \\
    -H 'content-type: application/json' \\
    -d '{"device_id":"pump-01","readings":[10,10.1,10,10.2,10,10.1,10,500]}'

Watch the HPA react to load:

  kubectl -n $NAMESPACE get hpa scorer -w
  make load-test

Tear down with: make kind-down
EOF
