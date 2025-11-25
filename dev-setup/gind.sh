#!/usr/bin/env bash
# SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
#
# SPDX-License-Identifier: Apache-2.0

set -o errexit
set -o pipefail

COMMAND="${1:-up}"
VALID_COMMANDS=("up" "down")

COMPOSE_FILE=$(dirname "$0")/gind/docker-compose.yaml

case "$COMMAND" in
  up)
    docker compose -f "$COMPOSE_FILE" up -d --build

    make gardenadm-up SCENARIO=gind SKAFFOLD_PLATFORM="linux/$(go env GOARCH)" SKAFFOLD_CHECK_CLUSTER_NODE_PLATFORMS=false

    docker compose -f "$COMPOSE_FILE" exec control-plane bash -c '/install-gardenadm.sh $(cat /gardenadm/.skaffold-image) && gardenadm init -d /gardenadm/gind --skip-etcd-druid'

    docker compose -f "$COMPOSE_FILE" cp control-plane:/etc/kubernetes/admin.conf dev/kubeconfig-gind
    sed -i 's/api.root.garden.local.gardener.cloud/localhost:6443/' dev/kubeconfig-gind

    export KUBECONFIG=dev/kubeconfig-gind
    kubectl taint node --all node-role.kubernetes.io/control-plane- || true

#    make operator-up garden-up KUBECONFIG=$KUBECONFIG
    ;;

  down)
    docker compose -f "$COMPOSE_FILE" down
    ;;

  *)
    echo "Error: Invalid command '${COMMAND}'. Valid options are: ${VALID_COMMANDS[*]}." >&2
    exit 1
   ;;
esac
