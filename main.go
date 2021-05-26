package main

import (
	"context"
	"os"
	"time"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/logger"
	"github.com/gardener/gardener/pkg/operation/botanist/component/extensions/infrastructure"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

func main() {
	restConfig := config.GetConfigOrDie()
	c, err := client.New(restConfig, client.Options{Scheme: kubernetes.SeedScheme})
	utilruntime.Must(err)

	ctx := signals.SetupSignalHandler()

	namespace := "apply-test"
	err = c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
	if !apierrors.IsAlreadyExists(err) {
		utilruntime.Must(err)
	}

	logger.AddWriter(logger.NewLogger("info"), os.Stdout)
	applyInfrastructure(ctx, c, namespace)
}

func applyInfrastructure(ctx context.Context, c client.Client, namespace string) {
	providerConfig := &runtime.RawExtension{Raw: []byte(`{
	"apiVersion": "aws.provider.extensions.gardener.cloud/v1alpha1",
	"kind": "InfrastructureConfig",
	"networks": {
		"vpc": {
			"cidr": "10.250.0.0/16"
		},
		"zones": [
			{
				"internal": "10.250.112.0/22",
				"name": "eu-west-1c",
				"public": "10.250.96.0/22",
				"workers": "10.250.0.0/19"
			},
			{
				"internal": "10.250.212.0/22",
				"name": "eu-west-1b",
				"public": "10.250.96.0/22",
				"workers": "10.250.0.0/19"
			}
		]
	}
}
`)}

	values := &infrastructure.Values{
		Namespace:         namespace,
		Name:              "infra",
		Type:              "test",
		Region:            "eu-west-1",
		SSHPublicKey:      []byte(`some-public-key`),
		AnnotateOperation: true,
	}

	newInfra := func() infrastructure.Interface {
		values.ProviderConfig = providerConfig.DeepCopy()
		return infrastructure.New(logger.Logger, c, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
	}

	// deploy
	infraComponent := newInfra()
	utilruntime.Must(infraComponent.Deploy(ctx))

	// add status
	infra, err := infraComponent.Get(ctx)
	utilruntime.Must(err)
	patch := client.MergeFrom(infra.DeepCopy())
	delete(infra.Annotations, v1beta1constants.GardenerOperation)
	utilruntime.Must(c.Patch(ctx, infra, patch, client.FieldOwner("extension-provider-test")))

	patch = client.MergeFrom(infra.DeepCopy())
	infra.Status = extensionsv1alpha1.InfrastructureStatus{
		DefaultStatus: extensionsv1alpha1.DefaultStatus{
			ObservedGeneration: infra.Generation,
			LastOperation: &gardencorev1beta1.LastOperation{
				Description:    "Infra succeeded",
				LastUpdateTime: metav1.Now(),
				Progress:       100,
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				Type:           gardencorev1beta1.LastOperationTypeReconcile,
			},
			// LastError: &gardencorev1beta1.LastError{
			// 	Description: "foo",
			// 	Codes:       []gardencorev1beta1.ErrorCode{"INFR_DEPS"},
			// },
		},
	}
	utilruntime.Must(c.Status().Patch(ctx, infra, patch, client.FieldOwner("extension-provider-test")))

	// deploy again
	values.SSHPublicKey = nil
	infraComponent = newInfra()
	utilruntime.Must(infraComponent.Deploy(ctx))
}
