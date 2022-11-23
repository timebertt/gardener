#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

KIND_ENV="${KIND_ENV:-skaffold}"
KIND_NAME="${KIND_NAME:-local}"
CLUSTER_NAME="gardener-$KIND_NAME"
PATH_CLUSTER_VALUES="$(dirname "$0")/../example/gardener-local/kind/$KIND_NAME"
KUBECONFIG_COPY="$(dirname "$0")/../example/provider-local/seed-$(KIND_NAME)/$KIND_NAME"
PATH_KUBECONFIG=""

mkdir -m 0755 -p \
  "$(dirname "$0")/../dev/local-backupbuckets" \
  "$(dirname "$0")/../dev/local-registry"

kind create cluster \
  --name "$CLUSTER_NAME" \
  --config <(helm template $(dirname "$0")/../example/gardener-local/kind/cluster --values "$PATH_CLUSTER_VALUES" --set "environment=$KIND_ENV")

# workaround https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files
kubectl get nodes -o name |\
  cut -d/ -f2 |\
  xargs -I {} docker exec {} sh -c "sysctl fs.inotify.max_user_instances=8192"

if [[ "$KUBECONFIG" != "$PATH_KUBECONFIG" ]]; then
  cp "$KUBECONFIG" "$PATH_KUBECONFIG"
fi

if [[ -z "${SKIP_REGISTRY:-}" ]]; then
  kubectl apply -k "$(dirname "$0")/../example/gardener-local/registry"       --server-side
  kubectl wait --for=condition=available deployment -l app=registry -n registry --timeout 5m
fi
kubectl apply   -k "$(dirname "$0")/../example/gardener-local/calico"         --server-side
kubectl apply   -k "$(dirname "$0")/../example/gardener-local/metrics-server" --server-side

kubectl get nodes -l node-role.kubernetes.io/control-plane -o name |\
  cut -d/ -f2 |\
  xargs -I {} kubectl taint node {} node-role.kubernetes.io/master:NoSchedule- node-role.kubernetes.io/control-plane:NoSchedule- || true
