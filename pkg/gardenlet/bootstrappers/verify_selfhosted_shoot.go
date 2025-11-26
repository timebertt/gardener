// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package bootstrappers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenletutils "github.com/gardener/gardener/pkg/utils/gardener/gardenlet"
)

// VerifySelfHostedShootIsConnected only runs in the seed gardenlet. It checks if it is deployed in a self-hosted shoot
// and fails in case there is no corresponding Shoot object in the Gardener API yet.
// This ensures that `gardenadm connect` is called before deploying a seed gardenlet into a self-hosted shoot. This is
// required to correctly handle ControllerInstallations/extension deployments into the self-hosted shoot (to prevent
// that multiple instances of the same extension are deployed into the cluster).
func VerifySelfHostedShootIsConnected(ctx context.Context, gardenReader, seedReader client.Reader, shootKey client.ObjectKey) error {
	seedIsSelfHostedShoot, err := gardenletutils.SeedIsSelfHostedShoot(ctx, seedReader)
	if err != nil {
		return fmt.Errorf("failed checking if seed is self-hosted shoot: %w", err)
	}
	if !seedIsSelfHostedShoot {
		return nil
	}

	if err := gardenReader.Get(ctx, shootKey, &gardencorev1beta1.Shoot{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed checking if Shoot resource %q exists in Garden API: %w", shootKey, err)
		}
		return fmt.Errorf("the Shoot resource %q must exist in Garden API before deploying a seed gardenlet - run 'gardenadm connect' first", shootKey)
	}

	return nil
}
