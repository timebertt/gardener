// Copyright 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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
	"github.com/gardener/gardener/extensions/pkg/controller/backupupload"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type actuator struct {
	backupupload.Actuator
	client client.Client

	containerMountPath string
	backBucketPath     string
}

func newActuator(containerMountPath, backupBucketPath string) backupupload.Actuator {
	return &actuator{
		containerMountPath: containerMountPath,
		backBucketPath:     backupBucketPath,
	}
}

func (a *actuator) InjectClient(client client.Client) error {
	a.client = client
	return nil
}

func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, bu *extensionsv1alpha1.BackupUpload) error {
	be := &extensionsv1alpha1.BackupEntry{}

	err := a.client.Get(ctx, types.NamespacedName{Name: bu.Spec.EntryName, Namespace: bu.Namespace}, be)
	if err != nil {
		return err
	}

	log.Info("bu", "data", bu)
	log.Info("be", "data", be)
	log.Info("options", "backBucketPath", a.backBucketPath, "containerMountPath", a.containerMountPath)

	path := filepath.Join(a.backBucketPath, be.Spec.BucketName, be.Name, bu.Spec.FilePath)

	return os.WriteFile(path, bu.Spec.Data, 0644)
}

func (a *actuator) Delete(_ context.Context, _ logr.Logger, _ *extensionsv1alpha1.BackupUpload) error {
	return nil
}
