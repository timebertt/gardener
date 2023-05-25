// Copyright 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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
	"fmt"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	reconcilerutils "github.com/gardener/gardener/pkg/controllerutils/reconciler"
)

type reconciler struct {
	actuator Actuator

	client        client.Client
	reader        client.Reader
	statusUpdater extensionscontroller.StatusUpdater
}

// NewReconciler creates a new reconcile.Reconciler that reconciles
// BackupDownload resources of Gardener's `extensions.gardener.cloud` API group.
func NewReconciler(actuator Actuator) reconcile.Reconciler {
	return reconcilerutils.OperationAnnotationWrapper(
		func() client.Object { return &extensionsv1alpha1.BackupDownload{} },
		&reconciler{
			actuator:      actuator,
			statusUpdater: extensionscontroller.NewStatusUpdater(),
		},
	)
}

func (r *reconciler) InjectFunc(f inject.Func) error {
	return f(r.actuator)
}

func (r *reconciler) InjectClient(client client.Client) error {
	r.client = client
	r.statusUpdater.InjectClient(client)
	return nil
}

func (r *reconciler) InjectAPIReader(reader client.Reader) error {
	r.reader = reader
	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	bd := &extensionsv1alpha1.BackupDownload{}
	if err := r.client.Get(ctx, request.NamespacedName, bd); err != nil {
		if apierrors.IsNotFound(err) {
			log.V(1).Info("Object is gone, stop reconciling")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}

	if bd.DeletionTimestamp != nil {
		log.V(1).Info("Object is in deletion, stop reconciling")
		return reconcile.Result{}, nil
	}

	return r.reconcile(ctx, log, bd)
}

func (r *reconciler) reconcile(ctx context.Context, log logr.Logger, bd *extensionsv1alpha1.BackupDownload) (reconcile.Result, error) {
	operationType := v1beta1helper.ComputeOperationType(bd.ObjectMeta, bd.Status.LastOperation)
	if err := r.statusUpdater.Processing(ctx, log, bd, operationType, "Reconciling the BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	be := &extensionsv1alpha1.BackupEntry{}
	err := r.client.Get(ctx, types.NamespacedName{Name: bd.Spec.EntryName, Namespace: bd.Namespace}, be)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get BackupEntry: %w", err)
	}

	bb := &extensionsv1alpha1.BackupBucket{}
	err = r.client.Get(ctx, types.NamespacedName{Name: be.Spec.BucketName}, bb)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get BackupBucket: %w", err)
	}

	log.Info("Starting the reconciliation of BackupDownload")
	dataContent, err := r.actuator.Reconcile(ctx, log, bd, be, bb)
	if err != nil || dataContent == nil {
		_ = r.statusUpdater.Error(ctx, log, bd, reconcilerutils.ReconcileErrCauseOrErr(err), operationType, "Error reconciling BackupDownload")
		return reconcilerutils.ReconcileErr(err)
	}

	log.Info("Updating data in BackupDownload status")
	bd.Status.Data = dataContent
	if err = r.client.Status().Update(ctx, bd); err != nil {
		_ = r.statusUpdater.Error(ctx, log, bd, reconcilerutils.ReconcileErrCauseOrErr(err), operationType, "Error updating data status from BackupDownload")
		return reconcilerutils.ReconcileErr(err)
	}

	if err := r.statusUpdater.Success(ctx, log, bd, operationType, "Successfully reconciled BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}