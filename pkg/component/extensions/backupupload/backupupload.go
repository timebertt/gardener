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

package backupupload

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/gardener/gardener/pkg/controllerutils"
	"github.com/gardener/gardener/pkg/extensions"
)

const (
	// DefaultInterval is the default interval for retry operations.
	DefaultInterval = 5 * time.Second
	// DefaultSevereThreshold is the default threshold until an error reported by another component is treated as 'severe'.
	DefaultSevereThreshold = 30 * time.Second
	// DefaultTimeout is the default timeout and defines how long Gardener should wait
	// for a successful reconciliation of a BackupUpload resource.
	DefaultTimeout = 10 * time.Minute
)

// Values contains the values used to create a BackupUpload CRD
type Values struct {
	// Name is the name of the BackupUpload resource.
	Name string
	// Type is the type of BackupUpload plugin/extension.
	Type string
	// EntryName is the name of the referenced BackupUpload.
	EntryName string
	// FilePath is the path of the file that should be uploaded.
	FilePath string
	// Data is the data that should be uploaded.
	Data []byte
}

// New creates a new instance of Interface.
func New(
	log logr.Logger,
	client client.Client,
	namespace string,
	clock clock.Clock,
	values *Values,
	waitInterval time.Duration,
	waitSevereThreshold time.Duration,
	waitTimeout time.Duration,
) component.DeployWaiter {
	return &backupUpload{
		log:                 log,
		client:              client,
		namespace:           namespace,
		clock:               clock,
		values:              values,
		waitInterval:        waitInterval,
		waitSevereThreshold: waitSevereThreshold,
		waitTimeout:         waitTimeout,
	}
}

type backupUpload struct {
	log                 logr.Logger
	client              client.Client
	namespace           string
	clock               clock.Clock
	values              *Values
	waitInterval        time.Duration
	waitSevereThreshold time.Duration
	waitTimeout         time.Duration
}

// Deploy uses the seed client to create or update the BackupUpload custom resource in the Seed.
func (b *backupUpload) Deploy(ctx context.Context) error {
	upload := b.emptyBackupUpload()

	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, b.client, upload, func() error {
		metav1.SetMetaDataAnnotation(&upload.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationReconcile)
		metav1.SetMetaDataAnnotation(&upload.ObjectMeta, v1beta1constants.GardenerTimestamp, b.clock.Now().UTC().Format(time.RFC3339Nano))

		upload.Spec = extensionsv1alpha1.BackupUploadSpec{
			DefaultSpec: extensionsv1alpha1.DefaultSpec{
				Type: b.values.Type,
			},
			EntryName: b.values.EntryName,
			FilePath:  b.values.FilePath,
			Data:      b.values.Data,
		}

		return nil
	})
	return err
}

// Destroy deletes the BackupUpload CRD.
func (b *backupUpload) Destroy(ctx context.Context) error {
	return extensions.DeleteExtensionObject(
		ctx,
		b.client,
		b.emptyBackupUpload(),
	)
}

// Wait waits until the BackupUpload CRD is ready.
func (b *backupUpload) Wait(ctx context.Context) error {
	return extensions.WaitUntilExtensionObjectReady(
		ctx,
		b.client,
		b.log, b.emptyBackupUpload(),
		extensionsv1alpha1.BackupUploadResource,
		b.waitInterval,
		b.waitSevereThreshold,
		b.waitTimeout,
		nil,
	)
}

// WaitCleanup waits until the BackupUpload CRD is deleted.
func (b *backupUpload) WaitCleanup(ctx context.Context) error {
	return extensions.WaitUntilExtensionObjectDeleted(
		ctx,
		b.client,
		b.log,
		b.emptyBackupUpload(),
		extensionsv1alpha1.BackupUploadResource,
		b.waitInterval,
		b.waitTimeout,
	)
}

func (b *backupUpload) emptyBackupUpload() *extensionsv1alpha1.BackupUpload {
	return &extensionsv1alpha1.BackupUpload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.values.Name,
			Namespace: b.namespace,
		},
	}
}
