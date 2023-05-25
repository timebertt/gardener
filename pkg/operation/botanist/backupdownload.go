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
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"

	"k8s.io/utils/clock"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/component/extensions/backupdownload"
)

// DownloadShootStateBackup deploys a BackupDownload resource for the shootstate. After success, it immediately
// deletes the BackupDownload resource again.
func (b *Botanist) DownloadShootStateBackup(ctx context.Context) error {
	if b.Seed.GetInfo().Spec.Backup == nil {
		return fmt.Errorf("cannot deploy BackupDownload for Shoot state since Seed is not configured with backup")
	}

	var (
		values = &backupdownload.Values{
			Name:      "shootstate",
			Type:      b.Seed.GetInfo().Spec.Backup.Provider,
			EntryName: b.Shoot.BackupEntryName,
			FilePath:  "shootstate",
		}
		deployer = backupdownload.New(
			b.Logger,
			b.SeedClientSet.Client(),
			b.Shoot.SeedNamespace,
			clock.RealClock{},
			values,
			backupdownload.DefaultInterval,
			backupdownload.DefaultSevereThreshold,
			backupdownload.DefaultTimeout,
		)
	)

	// Make sure no `BackupDownload`s exist anymore before deploying a new one.
	if err := component.OpDestroyAndWait(deployer).Destroy(ctx); err != nil {
		return err
	}
	if err := component.OpWait(deployer).Deploy(ctx); err != nil {
		return err
	}
	if err := b.loadShootState(deployer.GetData()); err != nil {
		return err
	}
	return component.OpDestroyAndWait(deployer).Destroy(ctx)
}

func (b *Botanist) loadShootState(data []byte) error {
	raw, err := decrypt(cipherKey, data)
	if err != nil {
		return fmt.Errorf("failed decrypting ShootState: %w", err)
	}

	shootState := &gardencorev1beta1.ShootState{}
	if err := json.Unmarshal(raw, shootState); err != nil {
		return fmt.Errorf("failed unmarshaling raw ShootState: %w", err)
	}

	b.Shoot.SetShootState(shootState)
	return nil
}

func decrypt(key, data []byte) ([]byte, error) {
	// Create a new AES cipher with the key and encrypted message
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// IF the length of the cipherText is less than 16 Bytes:
	if len(data) < aes.BlockSize {
		return nil, errors.New("Ciphertext block size is too short!")
	}

	iv := data[:aes.BlockSize]
	data = data[aes.BlockSize:]

	// Decrypt the message
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(data, data)

	return data, nil
}
