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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/controllerutils"
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
		return r.delete(ctx, log, bd)
	}

	return r.reconcile(ctx, log, bd)
}

func (r *reconciler) reconcile(ctx context.Context, log logr.Logger, bd *extensionsv1alpha1.BackupDownload) (reconcile.Result, error) {
	if !controllerutil.ContainsFinalizer(bd, FinalizerName) {
		log.Info("Adding finalizer")
		if err := controllerutils.AddFinalizers(ctx, r.client, bd, FinalizerName); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	operationType := v1beta1helper.ComputeOperationType(bd.ObjectMeta, bd.Status.LastOperation)
	if err := r.statusUpdater.Processing(ctx, log, bd, operationType, "Reconciling the BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	log.Info("Starting the reconciliation of BackupDownload")
	if err := r.actuator.Reconcile(ctx, log, bd); err != nil {
		_ = r.statusUpdater.Error(ctx, log, bd, reconcilerutils.ReconcileErrCauseOrErr(err), operationType, "Error reconciling BackupDownload")
		return reconcilerutils.ReconcileErr(err)
	}

	if err := r.statusUpdater.Success(ctx, log, bd, operationType, "Successfully reconciled BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *reconciler) delete(ctx context.Context, log logr.Logger, bd *extensionsv1alpha1.BackupDownload) (reconcile.Result, error) {
	if !controllerutil.ContainsFinalizer(bd, FinalizerName) {
		log.Info("Deleting BackupDownload causes a no-op as there is no finalizer")
		return reconcile.Result{}, nil
	}

	operationType := v1beta1helper.ComputeOperationType(bd.ObjectMeta, bd.Status.LastOperation)
	if err := r.statusUpdater.Processing(ctx, log, bd, operationType, "Deleting the BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	log.Info("Starting the deletion of BackupDownload")
	if err := r.actuator.Delete(ctx, log, bd); err != nil {
		_ = r.statusUpdater.Error(ctx, log, bd, reconcilerutils.ReconcileErrCauseOrErr(err), operationType, "Error deleting BackupDownload")
		return reconcilerutils.ReconcileErr(err)
	}

	if err := r.statusUpdater.Success(ctx, log, bd, operationType, "Successfully deleted BackupDownload"); err != nil {
		return reconcile.Result{}, err
	}

	if controllerutil.ContainsFinalizer(bd, FinalizerName) {
		log.Info("Removing finalizer")
		if err := controllerutils.RemoveFinalizers(ctx, r.client, bd, FinalizerName); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	return reconcile.Result{}, nil
}
