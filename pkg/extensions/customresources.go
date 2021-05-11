// Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package extensions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1alpha1helper "github.com/gardener/gardener/pkg/apis/core/v1alpha1/helper"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	gardencorev1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/utils"
	"github.com/gardener/gardener/pkg/utils/flow"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/kubernetes/health"
	"github.com/gardener/gardener/pkg/utils/retry"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/sirupsen/logrus"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TimeNow returns the current time. Exposed for testing.
var TimeNow = time.Now

// WaitUntilExtensionObjectReady waits until the given extension object has become ready.
// Passed objects are expected to be filled with the latest state the controller/component
// applied/observed/retrieved, but at least namespace and name.
func WaitUntilExtensionObjectReady(
	ctx context.Context,
	c client.Client,
	logger logrus.FieldLogger,
	obj extensionsv1alpha1.Object,
	kind string,
	interval time.Duration,
	severeThreshold time.Duration,
	timeout time.Duration,
	postReadyFunc func() error,
) error {
	var healthFuncs []health.Func

	// If the extension object has been reconciled successfully before we triggered a new reconciliation and our cache
	// is not updated fast enough with our reconciliation trigger (i.e. adding the reconcile annotation), we might
	// falsy return early from waiting for the extension object to be ready (as the last state already was "ready").
	// Use the timestamp annotation on the object as an ensurance, that once we see it in our cache, we are observing
	// a version of the object that is fresh enough.
	if expectedTimestamp, ok := obj.GetAnnotations()[v1beta1constants.GardenerTimestamp]; ok {
		healthFuncs = append(healthFuncs, health.ObjectHasAnnotationWithValue(v1beta1constants.GardenerTimestamp, expectedTimestamp))
	}

	healthFuncs = append(healthFuncs, health.CheckExtensionObject)

	return WaitUntilObjectReadyWithHealthFunction(
		ctx,
		c,
		logger,
		health.And(healthFuncs...),
		obj,
		kind,
		interval,
		severeThreshold,
		timeout,
		postReadyFunc,
	)
}

// WaitUntilObjectReadyWithHealthFunction waits until the given object has become ready. It takes the health check
// function that should be executed.
// Passed objects are expected to be filled with the latest state the controller/component
// observed/retrieved, but at least namespace and name.
func WaitUntilObjectReadyWithHealthFunction(
	ctx context.Context,
	c client.Client,
	logger logrus.FieldLogger,
	healthFunc health.Func,
	obj client.Object,
	kind string,
	interval time.Duration,
	severeThreshold time.Duration,
	timeout time.Duration,
	postReadyFunc func() error,
) error {
	var (
		errorWithCode         *gardencorev1beta1helper.ErrorWithCodes
		lastObservedError     error
		retryCountUntilSevere int

		name      = obj.GetName()
		namespace = obj.GetNamespace()
	)

	resetObj, err := createResetObjectFunc(obj, c.Scheme())
	if err != nil {
		return err
	}

	if err := retry.UntilTimeout(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		retryCountUntilSevere++

		resetObj()
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj); err != nil {
			if apierrors.IsNotFound(err) {
				return retry.MinorError(err)
			}
			return retry.SevereError(err)
		}

		if err := healthFunc(obj); err != nil {
			lastObservedError = err
			logger.WithError(err).Errorf("%s did not get ready yet", extensionKey(kind, namespace, name))
			if errors.As(err, &errorWithCode) {
				return retry.MinorOrSevereError(retryCountUntilSevere, int(severeThreshold.Nanoseconds()/interval.Nanoseconds()), err)
			}
			return retry.MinorError(err)
		}

		if postReadyFunc != nil {
			if err := postReadyFunc(); err != nil {
				return retry.SevereError(err)
			}
		}

		return retry.Ok()
	}); err != nil {
		message := fmt.Sprintf("Error while waiting for %s to become ready", extensionKey(kind, namespace, name))
		if lastObservedError != nil {
			return gardencorev1beta1helper.NewErrorWithCodes(formatErrorMessage(message, lastObservedError.Error()), gardencorev1beta1helper.ExtractErrorCodes(lastObservedError)...)
		}
		return errors.New(formatErrorMessage(message, err.Error()))
	}

	return nil
}

// DeleteExtensionObject deletes a given extension object.
// Passed objects are expected to be filled with the latest state the controller/component
// observed/retrieved, but at least namespace and name.
func DeleteExtensionObject(
	ctx context.Context,
	c client.Writer,
	obj extensionsv1alpha1.Object,
	deleteOpts ...client.DeleteOption,
) error {
	if err := gutil.ConfirmDeletion(ctx, c, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return client.IgnoreNotFound(c.Delete(ctx, obj, deleteOpts...))
}

// DeleteExtensionObjects lists all extension objects and loops over them. It executes the given <predicateFunc> for
// each of them, and if it evaluates to true then the object will be deleted.
func DeleteExtensionObjects(
	ctx context.Context,
	c client.Client,
	listObj client.ObjectList,
	namespace string,
	predicateFunc func(obj extensionsv1alpha1.Object) bool,
	deleteOpts ...client.DeleteOption,
) error {
	fns, err := applyFuncToExtensionObjects(ctx, c, listObj, namespace, predicateFunc, func(ctx context.Context, obj extensionsv1alpha1.Object) error {
		return DeleteExtensionObject(
			ctx,
			c,
			obj,
			deleteOpts...,
		)
	})
	if err != nil {
		return err
	}

	return flow.Parallel(fns...)(ctx)
}

// WaitUntilExtensionObjectsDeleted lists all extension objects and loops over them. It executes the given
// <predicateFunc> for each of them, and if it evaluates to true and the object is already marked for deletion,
// then it waits for the object to be deleted.
func WaitUntilExtensionObjectsDeleted(
	ctx context.Context,
	c client.Client,
	logger logrus.FieldLogger,
	listObj client.ObjectList,
	kind string,
	namespace string,
	interval time.Duration,
	timeout time.Duration,
	predicateFunc func(obj extensionsv1alpha1.Object) bool,
) error {
	fns, err := applyFuncToExtensionObjects(
		ctx,
		c,
		listObj,
		namespace,
		func(obj extensionsv1alpha1.Object) bool {
			if obj.GetDeletionTimestamp() == nil {
				return false
			}
			if predicateFunc != nil && !predicateFunc(obj) {
				return false
			}
			return true
		},
		func(ctx context.Context, obj extensionsv1alpha1.Object) error {
			return WaitUntilExtensionObjectDeleted(
				ctx,
				c,
				logger,
				obj,
				kind,
				interval,
				timeout,
			)
		},
	)
	if err != nil {
		return err
	}

	return flow.Parallel(fns...)(ctx)
}

// WaitUntilExtensionObjectDeleted waits until an extension oject is deleted from the system.
// Passed objects are expected to be filled with the latest state the controller/component
// observed/retrieved, but at least namespace and name.
func WaitUntilExtensionObjectDeleted(
	ctx context.Context,
	c client.Client,
	logger logrus.FieldLogger,
	obj extensionsv1alpha1.Object,
	kind string,
	interval time.Duration,
	timeout time.Duration,
) error {
	var (
		lastObservedError error

		name      = obj.GetName()
		namespace = obj.GetNamespace()
	)

	resetObj, err := createResetObjectFunc(obj, c.Scheme())
	if err != nil {
		return err
	}

	if err := retry.UntilTimeout(ctx, interval, timeout, func(ctx context.Context) (bool, error) {
		resetObj()
		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj); err != nil {
			if apierrors.IsNotFound(err) {
				return retry.Ok()
			}
			return retry.SevereError(err)
		}

		if lastErr := obj.GetExtensionStatus().GetLastError(); lastErr != nil {
			logger.Errorf("%s did not get deleted yet, lastError is: %s", extensionKey(kind, namespace, name), lastErr.Description)
			lastObservedError = gardencorev1beta1helper.NewErrorWithCodes(lastErr.Description, lastErr.Codes...)
		}

		var message = fmt.Sprintf("%s is still present", extensionKey(kind, namespace, name))
		if lastObservedError != nil {
			message += fmt.Sprintf(", last observed error: %s", lastObservedError.Error())
		}
		return retry.MinorError(fmt.Errorf(message))
	}); err != nil {
		message := fmt.Sprintf("Failed to delete %s", extensionKey(kind, namespace, name))
		if lastObservedError != nil {
			return gardencorev1beta1helper.NewErrorWithCodes(formatErrorMessage(message, lastObservedError.Error()), gardencorev1beta1helper.ExtractErrorCodes(lastObservedError)...)
		}
		return errors.New(formatErrorMessage(message, err.Error()))
	}

	return nil
}

// RestoreExtensionWithDeployFunction deploys the extension object with the passed in deployFunc and sets its operation annotation to wait-for-state.
// It then restores the state of the extension object from the ShootState, creates any required state object and sets the operation annotation to restore.
func RestoreExtensionWithDeployFunction(
	ctx context.Context,
	c client.Client,
	shootState *gardencorev1alpha1.ShootState,
	kind string,
	deployFunc func(ctx context.Context, operationAnnotation string) (extensionsv1alpha1.Object, error),
) error {
	extensionObj, err := deployFunc(ctx, v1beta1constants.GardenerOperationWaitForState)
	if err != nil {
		return err
	}

	if err := RestoreExtensionObjectState(ctx, c, shootState, extensionObj, kind); err != nil {
		return err
	}

	return AnnotateObjectWithOperation(ctx, c, extensionObj, v1beta1constants.GardenerOperationRestore)
}

// RestoreExtensionObjectState restores the status.state field of the extension objects and deploys any required objects from the provided shoot state
func RestoreExtensionObjectState(
	ctx context.Context,
	c client.Client,
	shootState *gardencorev1alpha1.ShootState,
	extensionObj extensionsv1alpha1.Object,
	kind string,
) error {
	var resourceRefs []autoscalingv1.CrossVersionObjectReference
	if shootState.Spec.Extensions != nil {
		resourceName := extensionObj.GetName()
		purpose := extensionObj.GetExtensionSpec().GetExtensionPurpose()
		list := gardencorev1alpha1helper.ExtensionResourceStateList(shootState.Spec.Extensions)
		if extensionResourceState := list.Get(kind, &resourceName, purpose); extensionResourceState != nil {
			patch := client.MergeFrom(extensionObj.DeepCopyObject())
			extensionStatus := extensionObj.GetExtensionStatus()
			extensionStatus.SetState(extensionResourceState.State)
			extensionStatus.SetResources(extensionResourceState.Resources)

			if err := c.Status().Patch(ctx, extensionObj, patch); err != nil {
				return err
			}

			for _, r := range extensionResourceState.Resources {
				resourceRefs = append(resourceRefs, r.ResourceRef)
			}
		}
	}
	if shootState.Spec.Resources != nil {
		list := gardencorev1alpha1helper.ResourceDataList(shootState.Spec.Resources)
		for _, resourceRef := range resourceRefs {
			resourceData := list.Get(&resourceRef)
			if resourceData != nil {
				obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&resourceData.Data)
				if err != nil {
					return err
				}
				if err := utils.CreateOrUpdateObjectByRef(ctx, c, &resourceRef, extensionObj.GetNamespace(), obj); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// MigrateExtensionObject adds the migrate operation annotation to the extension object.
// Passed objects are expected to be filled with the latest state the controller/component
// observed/retrieved, but at least namespace and name.
func MigrateExtensionObject(
	ctx context.Context,
	c client.Writer,
	obj extensionsv1alpha1.Object,
) error {
	return client.IgnoreNotFound(AnnotateObjectWithOperation(ctx, c, obj, v1beta1constants.GardenerOperationMigrate))
}

// MigrateExtensionObjects lists all extension objects of a given kind and annotates them with the Migrate operation.
func MigrateExtensionObjects(
	ctx context.Context,
	c client.Client,
	listObj client.ObjectList,
	namespace string,
) error {
	fns, err := applyFuncToExtensionObjects(ctx, c, listObj, namespace, nil, func(ctx context.Context, obj extensionsv1alpha1.Object) error {
		return MigrateExtensionObject(ctx, c, obj)
	})
	if err != nil {
		return err
	}

	return flow.Parallel(fns...)(ctx)
}

// WaitUntilExtensionObjectMigrated waits until the migrate operation for the extension object is successful.
// Passed objects are expected to be filled with the latest state the controller/component
// observed/retrieved, but at least namespace and name.
func WaitUntilExtensionObjectMigrated(
	ctx context.Context,
	c client.Client,
	obj extensionsv1alpha1.Object,
	interval time.Duration,
	timeout time.Duration,
) error {
	var (
		name      = obj.GetName()
		namespace = obj.GetNamespace()
	)

	resetObj, err := createResetObjectFunc(obj, c.Scheme())
	if err != nil {
		return err
	}

	return retry.UntilTimeout(ctx, interval, timeout, func(ctx context.Context) (done bool, err error) {
		resetObj()
		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, obj); err != nil {
			if client.IgnoreNotFound(err) == nil {
				return retry.Ok()
			}
			return retry.SevereError(err)
		}

		if extensionObjStatus := obj.GetExtensionStatus(); extensionObjStatus != nil {
			if lastOperation := extensionObjStatus.GetLastOperation(); lastOperation != nil {
				if lastOperation.Type == gardencorev1beta1.LastOperationTypeMigrate && lastOperation.State == gardencorev1beta1.LastOperationStateSucceeded {
					return retry.Ok()
				}
			}
		}

		var extensionType string
		if extensionSpec := obj.GetExtensionSpec(); extensionSpec != nil {
			extensionType = extensionSpec.GetExtensionType()
		}
		return retry.MinorError(fmt.Errorf("lastOperation for %s with name %s and type %s is not Migrate=Succeeded", obj.GetObjectKind().GroupVersionKind().Kind, name, extensionType))
	})
}

// WaitUntilExtensionObjectsMigrated lists all extension objects of a given kind and waits until they are migrated.
func WaitUntilExtensionObjectsMigrated(
	ctx context.Context,
	c client.Client,
	listObj client.ObjectList,
	namespace string,
	interval time.Duration,
	timeout time.Duration,
) error {
	fns, err := applyFuncToExtensionObjects(ctx, c, listObj, namespace, nil, func(ctx context.Context, obj extensionsv1alpha1.Object) error {
		return WaitUntilExtensionObjectMigrated(
			ctx,
			c,
			obj,
			interval,
			timeout,
		)
	})
	if err != nil {
		return err
	}

	return flow.Parallel(fns...)(ctx)
}

// AnnotateObjectWithOperation annotates the object with the provided operation annotation value.
func AnnotateObjectWithOperation(ctx context.Context, w client.Writer, obj client.Object, operation string) error {
	patch := client.MergeFrom(obj.DeepCopyObject())
	kutil.SetMetaDataAnnotation(obj, v1beta1constants.GardenerOperation, operation)
	kutil.SetMetaDataAnnotation(obj, v1beta1constants.GardenerTimestamp, TimeNow().UTC().String())
	return w.Patch(ctx, obj, patch)
}

func applyFuncToExtensionObjects(
	ctx context.Context,
	c client.Reader,
	listObj client.ObjectList,
	namespace string,
	predicateFunc func(obj extensionsv1alpha1.Object) bool,
	applyFunc func(ctx context.Context, object extensionsv1alpha1.Object) error,
) ([]flow.TaskFn, error) {
	if err := c.List(ctx, listObj, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	fns := make([]flow.TaskFn, 0, meta.LenList(listObj))

	if err := meta.EachListItem(listObj, func(obj runtime.Object) error {
		o, ok := obj.(extensionsv1alpha1.Object)
		if !ok {
			return fmt.Errorf("expected extensionsv1alpha1.Object but got %T", obj)
		}

		if predicateFunc != nil && !predicateFunc(o) {
			return nil
		}

		fns = append(fns, func(ctx context.Context) error {
			return applyFunc(ctx, o)
		})

		return nil
	}); err != nil {
		return nil, err
	}

	return fns, nil
}

func extensionKey(kind, namespace, name string) string {
	return fmt.Sprintf("%s %s/%s", kind, namespace, name)
}

func formatErrorMessage(message, description string) string {
	return fmt.Sprintf("%s: %s", message, description)
}

// createResetObjectFunc creates a func that will reset the given object to a new empty object every time the func is called.
// This is useful for resetting an in-memory object before re-getting it from the API server / cache
// to avoid executing checks on stale/removed object data e.g. annotations/lastError
// (json decoder does not unset fields in the in-memory object that are unset in the API server's response)
func createResetObjectFunc(obj runtime.Object, scheme *runtime.Scheme) (func(), error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, err
	}
	emptyObj, err := scheme.New(gvk)
	if err != nil {
		return nil, err
	}
	return func() {
		deepCopyIntoObject(obj, emptyObj)
	}, nil
}

// deepCopyIntoObject deep copies src into dest.
// This is a workaround for runtime.Object's lack of a DeepCopyInto method, similar to what the c-r cache does:
// https://github.com/kubernetes-sigs/controller-runtime/blob/55a329c15d6b4f91a9ff072fed6f6f05ff3339e7/pkg/cache/internal/cache_reader.go#L85-L90
func deepCopyIntoObject(dest, src runtime.Object) {
	reflect.ValueOf(dest).Elem().Set(reflect.ValueOf(src.DeepCopyObject()).Elem())
}
