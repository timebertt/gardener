// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gardener/gardener/pkg/api/indexer"
	gardencore "github.com/gardener/gardener/pkg/apis/core"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/gardener/gardener/pkg/apis/seedmanagement"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	"github.com/gardener/gardener/pkg/controllermanager/apis/config"
	"github.com/gardener/gardener/pkg/controllermanager/controller/cloudprofile"
)

// AddControllersToManager adds all controller-manager controllers to the given manager.
func AddControllersToManager(mgr manager.Manager, cfg *config.ControllerManagerConfiguration) error {
	if err := (&cloudprofile.Reconciler{
		Config: cfg.Controllers.CloudProfile,
	}).AddToManager(mgr); err != nil {
		return fmt.Errorf("failed adding CloudProfile controller: %w", err)
	}

	return nil
}

// AddAllFieldIndexes adds all field indexes used by gardener-controller-manager to the given FieldIndexer (i.e. cache).
// field indexes have to be added before the cache is started (i.e. before the manager is started)
func AddAllFieldIndexes(ctx context.Context, i client.FieldIndexer) error {
	for _, fn := range []func(context.Context, client.FieldIndexer) error{
		indexer.AddBastionShootName,
	} {
		if err := fn(ctx, i); err != nil {
			return err
		}
	}

	if err := i.IndexField(ctx, &gardencorev1beta1.Project{}, gardencore.ProjectNamespace, func(obj client.Object) []string {
		project, ok := obj.(*gardencorev1beta1.Project)
		if !ok {
			return []string{""}
		}
		if project.Spec.Namespace == nil {
			return []string{""}
		}
		return []string{*project.Spec.Namespace}
	}); err != nil {
		return fmt.Errorf("failed to add indexer to Project Informer: %w", err)
	}

	if err := i.IndexField(ctx, &gardencorev1beta1.Shoot{}, gardencore.ShootSeedName, func(obj client.Object) []string {
		shoot, ok := obj.(*gardencorev1beta1.Shoot)
		if !ok {
			return []string{""}
		}
		if shoot.Spec.SeedName == nil {
			return []string{""}
		}
		return []string{*shoot.Spec.SeedName}
	}); err != nil {
		return fmt.Errorf("failed to add indexer to Shoot Informer: %w", err)
	}

	if err := i.IndexField(ctx, &seedmanagementv1alpha1.ManagedSeed{}, seedmanagement.ManagedSeedShootName, func(obj client.Object) []string {
		ms, ok := obj.(*seedmanagementv1alpha1.ManagedSeed)
		if !ok {
			return []string{""}
		}
		if ms.Spec.Shoot == nil {
			return []string{""}
		}
		return []string{ms.Spec.Shoot.Name}
	}); err != nil {
		return fmt.Errorf("failed to add indexer to ManagedSeed Informer: %w", err)
	}

	if err := i.IndexField(ctx, &gardencorev1beta1.BackupBucket{}, gardencore.BackupBucketSeedName, func(obj client.Object) []string {
		backupBucket, ok := obj.(*gardencorev1beta1.BackupBucket)
		if !ok {
			return []string{""}
		}
		if backupBucket.Spec.SeedName == nil {
			return []string{""}
		}
		return []string{*backupBucket.Spec.SeedName}
	}); err != nil {
		return fmt.Errorf("failed to add indexer to BackupBucket Informer: %w", err)
	}

	if err := i.IndexField(ctx, &gardencorev1beta1.ControllerInstallation{}, gardencore.SeedRefName, func(obj client.Object) []string {
		controllerInstallation, ok := obj.(*gardencorev1beta1.ControllerInstallation)
		if !ok {
			return []string{""}
		}
		return []string{controllerInstallation.Spec.SeedRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to add indexer to ControllerInstallation Informer: %w", err)
	}

	return nil
}
