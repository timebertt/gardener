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

package backupdownload

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
	// for a successful reconciliation of a BackupDownload resource.
	DefaultTimeout = 10 * time.Minute
)

// Values contains the values used to create a BackupDownload CRD
type Values struct {
	// Name is the name of the BackupDownload resource.
	Name string
	// Type is the type of BackupDownload plugin/extension.
	Type string
	// EntryName is the name of the referenced BackupDownload.
	EntryName string
	// FilePath is the path of the file that should be downloaded.
	FilePath string
	// Data is the data that should be downloaded.
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
	return &backupDownload{
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

type backupDownload struct {
	log                 logr.Logger
	client              client.Client
	namespace           string
	clock               clock.Clock
	values              *Values
	waitInterval        time.Duration
	waitSevereThreshold time.Duration
	waitTimeout         time.Duration
}

// Deploy uses the seed client to create or update the BackupDownload custom resource in the Seed.
func (b *backupDownload) Deploy(ctx context.Context) error {
	download := b.emptyBackupDownload()

	_, err := controllerutils.GetAndCreateOrMergePatch(ctx, b.client, download, func() error {
		metav1.SetMetaDataAnnotation(&download.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationReconcile)
		metav1.SetMetaDataAnnotation(&download.ObjectMeta, v1beta1constants.GardenerTimestamp, b.clock.Now().UTC().Format(time.RFC3339Nano))

		download.Spec = extensionsv1alpha1.BackupDownloadSpec{
			DefaultSpec: extensionsv1alpha1.DefaultSpec{
				Type: b.values.Type,
			},
			EntryName: b.values.EntryName,
			FilePath:  b.values.FilePath,
		}

		return nil
	})
	return err
}

// Destroy deletes the BackupDownload CRD.
func (b *backupDownload) Destroy(ctx context.Context) error {
	return extensions.DeleteExtensionObject(
		ctx,
		b.client,
		b.emptyBackupDownload(),
	)
}

// Wait waits until the BackupDownload CRD is ready.
func (b *backupDownload) Wait(ctx context.Context) error {
	return extensions.WaitUntilExtensionObjectReady(
		ctx,
		b.client,
		b.log, b.emptyBackupDownload(),
		extensionsv1alpha1.BackupDownloadResource,
		b.waitInterval,
		b.waitSevereThreshold,
		b.waitTimeout,
		nil,
	)
}

// WaitCleanup waits until the BackupDownload CRD is deleted.
func (b *backupDownload) WaitCleanup(ctx context.Context) error {
	return extensions.WaitUntilExtensionObjectDeleted(
		ctx,
		b.client,
		b.log,
		b.emptyBackupDownload(),
		extensionsv1alpha1.BackupDownloadResource,
		b.waitInterval,
		b.waitTimeout,
	)
}

func (b *backupDownload) emptyBackupDownload() *extensionsv1alpha1.BackupDownload {
	return &extensionsv1alpha1.BackupDownload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.values.Name,
			Namespace: b.namespace,
		},
	}
}
