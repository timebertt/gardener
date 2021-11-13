#!/usr/bin/env bash

trap shoot_deletion EXIT

function shoot_deletion {
  go test -mod=vendor -timeout=0 ./test/system/shoot_deletion \
    --v -ginkgo.v -ginkgo.progress \
    -kubecfg=$KUBECONFIG \
    -project-namespace=garden-local \
    -shoot-name=e2e-local
}

go test -mod=vendor -timeout=0 ./test/system/shoot_creation \
  --v -ginkgo.v -ginkgo.progress \
  -kubecfg=$KUBECONFIG \
  -project-namespace=garden-local \
  -shoot-name=e2e-local \
  -k8s-version=1.21.0 \
  -cloud-profile=local \
  -seed=local \
  -region=local \
  -secret-binding=local \
  -provider-type=local \
  -networking-type=local \
  -worker-minimum=1 \
  -worker-maximum=1 \
  -shoot-template-path=<(cat <<EOF
apiVersion: core.gardener.cloud/v1beta1
kind: Shoot
EOF
)
