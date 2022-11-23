#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

KIND_ENV="${KIND_ENV:-skaffold}"
KIND_NAME="${KIND_NAME:-local}"
CLUSTER_NAME="gardener-$KIND_NAME"
KUBECONFIG_COPY=""

kind delete cluster \
  --name "$CLUSTER_NAME"

rm -f "$KUBECONFIG_COPY"

if [[ "$KIND_NAME" == "local" ]]; then
  rm -rf "$(dirname "$0")/../dev/local-backupbuckets"
fi
