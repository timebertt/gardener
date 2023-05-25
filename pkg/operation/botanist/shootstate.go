// Copyright 2023 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package botanist

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenerutils "github.com/gardener/gardener/pkg/utils/gardener"
)

// EnsureShootStateExists creates the ShootState resource for the corresponding shoot and updates the operations object
func (b *Botanist) EnsureShootStateExists(ctx context.Context) error {
	if b.isRestorePhase() {
		return b.DownloadShootStateBackup(ctx)
	}

	var (
		err        error
		shootState = &gardencorev1beta1.ShootState{
			ObjectMeta: metav1.ObjectMeta{
				Name:      b.Shoot.GetInfo().Name,
				Namespace: b.Shoot.GetInfo().Namespace,
			},
		}
	)

	if err = b.GardenClient.Create(ctx, shootState); client.IgnoreAlreadyExists(err) != nil {
		return err
	}

	if err = b.GardenClient.Get(ctx, client.ObjectKeyFromObject(shootState), shootState); err != nil {
		return err
	}
	b.SetShootState(shootState)

	return nil
}

// DeleteShootState deletes the ShootState resource for the corresponding shoot.
func (b *Botanist) DeleteShootState(ctx context.Context) error {
	shootState := &gardencorev1beta1.ShootState{
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.Shoot.GetInfo().Name,
			Namespace: b.Shoot.GetInfo().Namespace,
		},
	}

	if err := gardenerutils.ConfirmDeletion(ctx, b.GardenClient, shootState); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return client.IgnoreNotFound(b.GardenClient.Delete(ctx, shootState))
}

// GetShootState returns the shootstate resource of this Shoot in a concurrency safe way.
// This method should be used only for reading the data of the returned shootstate resource. The returned shootstate
// resource MUST NOT BE MODIFIED (except in test code) since this might interfere with other concurrent reads and writes.
// To properly update the shootstate resource of this Shoot use SaveGardenerResourceDataInShootState.
func (b *Botanist) GetShootState() *gardencorev1beta1.ShootState {
	return b.Shoot.GetShootState()
}

// SetShootState sets the shootstate resource of this Shoot in a concurrency safe way.
// This method is not protected by a mutex and does not update the shootstate resource in the cluster and so
// should be used only in exceptional situations, or as a convenience in test code. The shootstate passed as a parameter
// MUST NOT BE MODIFIED after the call to SetShootState (except in test code) since this might interfere with other concurrent reads and writes.
// To properly update the shootstate resource of this Shoot use SaveGardenerResourceDataInShootState.
func (b *Botanist) SetShootState(shootState *gardencorev1beta1.ShootState) {
	b.Shoot.SetShootState(shootState)
}

// SaveGardenerResourceDataInShootState updates the shootstate resource of this Shoot in a concurrency safe way,
// using the given context and mutate function.
// The mutate function should modify the passed GardenerResourceData so that changes are persisted.
// This method is protected by a mutex, so only a single SaveGardenerResourceDataInShootState operation can be
// executed at any point in time.
func (b *Botanist) SaveGardenerResourceDataInShootState(ctx context.Context, f func(*[]gardencorev1beta1.GardenerResourceData) error) error {
	shootState := b.Shoot.GetShootState().DeepCopy()
	original := shootState.DeepCopy()
	patch := client.StrategicMergeFrom(original)

	if err := f(&shootState.Spec.Gardener); err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(original.Spec.Gardener, shootState.Spec.Gardener) {
		return nil
	}
	if err := b.GardenClient.Patch(ctx, shootState, patch); err != nil {
		return err
	}
	b.Shoot.SetShootState(shootState)
	return nil
}
