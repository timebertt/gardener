```shell
make kind-up
export KUBECONFIG=$PWD/example/gardener-extension-provider-local/base/kubeconfig
make dev-setup
make start-apiserver
# wait for g-api to be available
make start-controller-manager
```

```shell
make register-local-env
```

```shell
make start-gardenlet SEED_NAME=local
make start-extension-provider-local
```

```shell
kubectl apply -f example/gardener-extension-provider-local/local
```

```shell
make test-e2e-local
```

```shell
make tear-down-local-env
```
