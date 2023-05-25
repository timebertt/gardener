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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/component/extensions/backupupload"
)

// DeployBackupUploadForShootState deploys a BackupUpload resource for the shootstate. After success, it immediately
// deletes the BackupUpload resource again.
func (b *Botanist) DeployBackupUploadForShootState(ctx context.Context) error {
	if b.Seed.GetInfo().Spec.Backup == nil {
		return fmt.Errorf("cannot deploy BackupUpload for Shoot state since Seed is not configured with backup")
	}

	data, err := b.computeDataForShootStateBackupUpload(ctx)
	if err != nil {
		return fmt.Errorf("failed computing data for BackupUpload of Shoot state: %w", err)
	}

	var (
		values = &backupupload.Values{
			Name:      "shootstate",
			Type:      b.Seed.GetInfo().Spec.Backup.Provider,
			EntryName: b.Shoot.BackupEntryName,
			FilePath:  "shootstate",
			Data:      data,
		}
		deployer = backupupload.New(
			b.Logger,
			b.SeedClientSet.Client(),
			b.Shoot.SeedNamespace,
			clock.RealClock{},
			values,
			backupupload.DefaultInterval,
			backupupload.DefaultSevereThreshold,
			backupupload.DefaultTimeout,
		)
	)

	if err := component.OpWait(deployer).Deploy(ctx); err != nil {
		return err
	}

	return client.IgnoreNotFound(b.SeedClientSet.Client().Delete(ctx, &extensionsv1alpha1.BackupUpload{ObjectMeta: metav1.ObjectMeta{Name: values.Name, Namespace: b.Shoot.SeedNamespace}}))
}

func (b *Botanist) computeDataForShootStateBackupUpload(ctx context.Context) ([]byte, error) {
	return nil, nil
}
