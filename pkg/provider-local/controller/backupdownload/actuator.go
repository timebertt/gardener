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

package backupdownload

import (
	"context"
	"github.com/gardener/gardener/extensions/pkg/controller/backupdownload"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

type actuator struct {
	backupdownload.Actuator
	client client.Client

	containerMountPath string
	backBucketPath     string
}

func newActuator(containerMountPath, backupBucketPath string) backupdownload.Actuator {
	return &actuator{
		containerMountPath: containerMountPath,
		backBucketPath:     backupBucketPath,
	}
}

func (a *actuator) InjectClient(client client.Client) error {
	a.client = client
	return nil
}

func (a *actuator) Reconcile(
	ctx context.Context,
	log logr.Logger,
	bd *extensionsv1alpha1.BackupDownload,
	be *extensionsv1alpha1.BackupEntry,
) ([]byte, error) {
	path := filepath.Join(a.backBucketPath, be.Spec.BucketName, strings.TrimPrefix(be.Name, v1beta1constants.BackupSourcePrefix+"-"), bd.Spec.FilePath)
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return file, nil
}
