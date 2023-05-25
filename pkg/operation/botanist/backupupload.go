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
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiextensions "github.com/gardener/gardener/pkg/api/extensions"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/component/extensions/backupupload"
	unstructuredutils "github.com/gardener/gardener/pkg/utils/kubernetes/unstructured"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
)

// UploadShootStateBackup deploys a BackupUpload resource for the shootstate. After success, it immediately
// deletes the BackupUpload resource again.
func (b *Botanist) UploadShootStateBackup(ctx context.Context) error {
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

	// Make sure no `BackupUpload`s exist anymore before deploying a new one.
	if err := component.OpDestroyAndWait(deployer).Destroy(ctx); err != nil {
		return err
	}
	if err := component.OpWait(deployer).Deploy(ctx); err != nil {
		return err
	}
	return component.OpDestroyAndWait(deployer).Destroy(ctx)
}

var cipherKey = []byte("asuperstrong32bitpasswordgohere!") // 32 bit key for AES-256

func (b *Botanist) computeDataForShootStateBackupUpload(ctx context.Context) ([]byte, error) {
	shootStateSpec, err := b.computeShootStateSpecForBackupUpload(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed computing ShootState spec for BackupUpload: %w", err)
	}

	raw, err := json.Marshal(&gardencorev1beta1.ShootState{Spec: *shootStateSpec})
	if err != nil {
		return nil, fmt.Errorf("failed marshaling ShootState spec to JSON: %w", err)
	}

	// TODO:
	//  - generate this key with secrets manager in garden cluster with 'keep old' and auto-rotation every 7d
	//  - store the key in project namespace in a `core.gardener.cloud/v1beta1.Secret` resource named <shoot>.state-encryption-key
	//  - this new Gardener resource can also contain the client CAs which are needed when eliminating the ShootState for
	//    adminkubeconfig generation
	//  - the manager should have a dedicated identity per shoot
	//  - the generation happens in this function right here
	//  - cleanup of secrets manager is called at the end of this function, again right here, after encryption succeeded
	//  - use owner ref to shoot in generated secrets by secrets manager

	return encrypt(cipherKey, raw)
}

func (b *Botanist) computeShootStateSpecForBackupUpload(ctx context.Context) (*gardencorev1beta1.ShootStateSpec, error) {
	gardener, err := b.computeShootStateGardenerData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed computing Gardener data: %w", err)
	}

	extensions, resources, err := b.computeShootStateExtensionsDataAndResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed computing extensions data and resources: %w", err)
	}

	return &gardencorev1beta1.ShootStateSpec{
		Gardener:   gardener,
		Extensions: extensions,
		Resources:  resources,
	}, nil
}

func (b *Botanist) computeShootStateGardenerData(ctx context.Context) ([]gardencorev1beta1.GardenerResourceData, error) {
	secretList := &corev1.SecretList{}
	if err := b.SeedClientSet.Client().List(ctx, secretList, client.InNamespace(b.Shoot.SeedNamespace), client.MatchingLabels{
		secretsmanager.LabelKeyManagedBy: secretsmanager.LabelValueSecretsManager,
		secretsmanager.LabelKeyPersist:   secretsmanager.LabelValueTrue,
	}); err != nil {
		return nil, fmt.Errorf("failed listing all secrets that must be persisted: %w", err)
	}

	var dataList v1beta1helper.GardenerResourceDataList

	for _, secret := range secretList.Items {
		dataJSON, err := json.Marshal(secret.Data)
		if err != nil {
			return nil, fmt.Errorf("failed marshalling secret data to JSON for secret %s: %w", client.ObjectKeyFromObject(&secret), err)
		}

		dataList.Upsert(&gardencorev1beta1.GardenerResourceData{
			Name:   secret.Name,
			Labels: secret.Labels,
			Type:   "secret",
			Data:   runtime.RawExtension{Raw: dataJSON},
		})
	}

	return dataList, nil
}

func (b *Botanist) computeShootStateExtensionsDataAndResources(ctx context.Context) ([]gardencorev1beta1.ExtensionResourceState, []gardencorev1beta1.ResourceData, error) {
	var (
		dataList  v1beta1helper.ExtensionResourceStateList
		resources v1beta1helper.ResourceDataList
	)

	for objKind, newObjectListFunc := range map[string]func() client.ObjectList{
		extensionsv1alpha1.BackupEntryResource:           func() client.ObjectList { return &extensionsv1alpha1.BackupEntryList{} },
		extensionsv1alpha1.ContainerRuntimeResource:      func() client.ObjectList { return &extensionsv1alpha1.ContainerRuntimeList{} },
		extensionsv1alpha1.ControlPlaneResource:          func() client.ObjectList { return &extensionsv1alpha1.ControlPlaneList{} },
		extensionsv1alpha1.DNSRecordResource:             func() client.ObjectList { return &extensionsv1alpha1.DNSRecordList{} },
		extensionsv1alpha1.ExtensionResource:             func() client.ObjectList { return &extensionsv1alpha1.ExtensionList{} },
		extensionsv1alpha1.InfrastructureResource:        func() client.ObjectList { return &extensionsv1alpha1.InfrastructureList{} },
		extensionsv1alpha1.NetworkResource:               func() client.ObjectList { return &extensionsv1alpha1.NetworkList{} },
		extensionsv1alpha1.OperatingSystemConfigResource: func() client.ObjectList { return &extensionsv1alpha1.OperatingSystemConfigList{} },
		extensionsv1alpha1.WorkerResource:                func() client.ObjectList { return &extensionsv1alpha1.WorkerList{} },
	} {
		objList := newObjectListFunc()
		if err := b.SeedClientSet.Client().List(ctx, objList, client.InNamespace(b.Shoot.SeedNamespace)); err != nil {
			return nil, nil, fmt.Errorf("failed to list extension resources of kind %s: %w", objKind, err)
		}

		if err := meta.EachListItem(objList, func(obj runtime.Object) error {
			extensionObj, err := apiextensions.Accessor(obj)
			if err != nil {
				return fmt.Errorf("failed accessing extension object: %w", err)
			}

			if extensionObj.GetDeletionTimestamp() != nil {
				return nil
			}

			dataList.Upsert(&gardencorev1beta1.ExtensionResourceState{
				Kind:      objKind,
				Name:      pointer.String(extensionObj.GetName()),
				Purpose:   extensionObj.GetExtensionSpec().GetExtensionPurpose(),
				State:     extensionObj.GetExtensionStatus().GetState(),
				Resources: extensionObj.GetExtensionStatus().GetResources(),
			})

			for _, newResource := range extensionObj.GetExtensionStatus().GetResources() {
				referencedObj, err := unstructuredutils.GetObjectByRef(ctx, b.SeedClientSet.Client(), &newResource.ResourceRef, b.Shoot.SeedNamespace)
				if err != nil {
					return fmt.Errorf("failed reading referenced object %s: %w", client.ObjectKey{Name: newResource.ResourceRef.Name, Namespace: b.Shoot.SeedNamespace}, err)
				}
				if obj == nil {
					return fmt.Errorf("object not found %v", newResource.ResourceRef)
				}

				raw := &runtime.RawExtension{}
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(referencedObj, raw); err != nil {
					return fmt.Errorf("failed converting referenced object %s to raw extension: %w", client.ObjectKey{Name: newResource.ResourceRef.Name, Namespace: b.Shoot.SeedNamespace}, err)
				}

				resources.Upsert(&gardencorev1beta1.ResourceData{
					CrossVersionObjectReference: newResource.ResourceRef,
					Data:                        *raw,
				})
			}

			return nil
		}); err != nil {
			return nil, nil, fmt.Errorf("failed computing extension data for kind %s: %w", objKind, err)
		}
	}

	return dataList, resources, nil
}

func encrypt(key, data []byte) ([]byte, error) {
	// Create a new AES cipher using the key
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	encryptedData := make([]byte, aes.BlockSize+len(data))

	// iv is the ciphertext up to the blocksize (16)
	iv := encryptedData[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	// Encrypt the data:
	stream := cipher.NewCFBEncrypter(block, iv)
	stream.XORKeyStream(encryptedData[aes.BlockSize:], data)

	return encryptedData, nil
}
