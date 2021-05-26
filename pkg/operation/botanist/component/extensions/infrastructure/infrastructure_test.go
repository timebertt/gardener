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

package infrastructure_test

import (
	"context"
	"errors"
	"time"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/logger"
	mockclient "github.com/gardener/gardener/pkg/mock/controller-runtime/client"
	mocktime "github.com/gardener/gardener/pkg/mock/go/time"
	"github.com/gardener/gardener/pkg/operation/botanist/component/extensions/infrastructure"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	"github.com/gardener/gardener/pkg/utils/retry"
	retryfake "github.com/gardener/gardener/pkg/utils/retry/fake"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("#Interface", func() {
	const (
		namespace    = "test-namespace"
		name         = "test-deploy"
		providerType = "foo"
	)

	var (
		ctx     context.Context
		log     logrus.FieldLogger
		fakeErr = errors.New("fake")

		ctrl    *gomock.Controller
		c       client.Client
		mockNow *mocktime.MockNow
		now     time.Time

		region         string
		sshPublicKey   []byte
		providerConfig *runtime.RawExtension
		providerStatus *runtime.RawExtension
		nodesCIDR      *string

		empty, expected *extensionsv1alpha1.Infrastructure
		values          *infrastructure.Values
		deployWaiter    infrastructure.Interface
		waiter          *retryfake.Ops

		cleanupFunc func()
	)

	BeforeEach(func() {
		ctx = context.TODO()
		log = logger.NewNopLogger()

		ctrl = gomock.NewController(GinkgoT())
		mockNow = mocktime.NewMockNow(ctrl)
		now = time.Now()

		s := runtime.NewScheme()
		Expect(extensionsv1alpha1.AddToScheme(s)).To(Succeed())
		c = fake.NewFakeClientWithScheme(s)

		region = "europe"
		sshPublicKey = []byte("secure")
		providerConfig = &runtime.RawExtension{Raw: []byte(`{"very":"provider-specific"}`)}
		providerStatus = &runtime.RawExtension{Raw: []byte(`{"very":"provider-specific-status"}`)}
		nodesCIDR = pointer.StringPtr("1.2.3.4/5")

		values = &infrastructure.Values{
			Namespace:      namespace,
			Name:           name,
			Type:           providerType,
			ProviderConfig: providerConfig,
			Region:         region,
		}

		empty = &extensionsv1alpha1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}

		expected = &extensionsv1alpha1.Infrastructure{
			TypeMeta: metav1.TypeMeta{
				APIVersion: extensionsv1alpha1.SchemeGroupVersion.String(),
				Kind:       extensionsv1alpha1.InfrastructureResource,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Annotations: map[string]string{
					v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
					v1beta1constants.GardenerTimestamp: now.UTC().String(),
				},
			},
			Spec: extensionsv1alpha1.InfrastructureSpec{
				DefaultSpec: extensionsv1alpha1.DefaultSpec{
					Type:           providerType,
					ProviderConfig: providerConfig,
				},
				Region:       region,
				SSHPublicKey: sshPublicKey,
				SecretRef: corev1.SecretReference{
					Name:      v1beta1constants.SecretNameCloudProvider,
					Namespace: namespace,
				},
			},
		}

		waiter = &retryfake.Ops{MaxAttempts: 1}
		cleanupFunc = test.WithVars(
			&retry.Until, waiter.Until,
			&retry.UntilTimeout, waiter.UntilTimeout,
		)

		deployWaiter = infrastructure.New(log, c, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
	})

	AfterEach(func() {
		ctrl.Finish()
		cleanupFunc()
	})

	Describe("#Deploy", func() {
		BeforeEach(func() {
			expected.ResourceVersion = "1"
		})

		It("correct Infrastructure is created (AnnotateOperation=false)", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			deployWaiter.SetSSHPublicKey(sshPublicKey)
			Expect(deployWaiter.Deploy(ctx)).To(Succeed())

			actual := &extensionsv1alpha1.Infrastructure{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(expected), actual)).To(Succeed())
			expected.SetAnnotations(nil)
			Expect(actual).To(DeepEqual(expected))
		})

		It("correct Infrastructure is created (AnnotateOperation=true)", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			values.AnnotateOperation = true
			deployWaiter.SetSSHPublicKey(sshPublicKey)
			Expect(deployWaiter.Deploy(ctx)).To(Succeed())

			actual := &extensionsv1alpha1.Infrastructure{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(expected), actual)).To(Succeed())
			Expect(actual).To(DeepEqual(expected))
		})
	})

	Describe("#Wait", func() {
		It("should return error when it's not found", func() {
			Expect(deployWaiter.Wait(ctx)).To(MatchError(ContainSubstring("not found")))
		})

		It("should return error when it's not ready", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.Wait(ctx)).To(MatchError(ContainSubstring("error during reconciliation: Some error")))
		})

		It("should return error if we haven't observed the latest timestamp annotation", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			By("deploy")
			// Deploy should fill internal state with the added timestamp annotation
			values.AnnotateOperation = true
			deployWaiter.SetSSHPublicKey(sshPublicKey)
			Expect(deployWaiter.Deploy(ctx)).To(Succeed())

			By("patch object")
			patch := client.MergeFrom(expected.DeepCopy())
			expected.Status.LastError = nil
			// remove operation annotation, add old timestamp annotation
			expected.ObjectMeta.Annotations = map[string]string{
				v1beta1constants.GardenerTimestamp: now.Add(-time.Millisecond).UTC().String(),
			}
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
			}
			Expect(c.Patch(ctx, expected, patch)).To(Succeed(), "patching infrastructure succeeds")

			By("wait")
			Expect(deployWaiter.Wait(ctx)).NotTo(Succeed(), "infrastructure indicates error")
		})

		It("should return no error when is ready", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			By("deploy")
			// Deploy should fill internal state with the added timestamp annotation
			values.AnnotateOperation = true
			deployWaiter.SetSSHPublicKey(sshPublicKey)
			Expect(deployWaiter.Deploy(ctx)).To(Succeed())

			By("patch object")
			patch := client.MergeFrom(expected.DeepCopy())
			expected.Status.LastError = nil
			// remove operation annotation, add up-to-date timestamp annotation
			expected.ObjectMeta.Annotations = map[string]string{
				v1beta1constants.GardenerTimestamp: now.UTC().String(),
			}
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
			}
			expected.Status.NodesCIDR = nodesCIDR
			expected.Status.ProviderStatus = providerStatus
			Expect(c.Patch(ctx, expected, patch)).To(Succeed(), "patching infrastructure succeeds")

			By("wait")
			Expect(deployWaiter.Wait(ctx)).To(Succeed(), "infrastructure is ready")

			By("verify status")
			Expect(deployWaiter.ProviderStatus()).To(Equal(providerStatus))
			Expect(deployWaiter.NodesCIDR()).To(Equal(nodesCIDR))
		})

		It("should return no error when is ready (AnnotateOperation == false)", func() {
			expected.Status.LastError = nil
			expected.ObjectMeta.Annotations = map[string]string{}
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
			}
			expected.Status.NodesCIDR = nodesCIDR
			expected.Status.ProviderStatus = providerStatus

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.Wait(ctx)).To(Succeed())
			Expect(deployWaiter.ProviderStatus()).To(Equal(providerStatus))
			Expect(deployWaiter.NodesCIDR()).To(Equal(nodesCIDR))
		})
	})

	Describe("#Destroy", func() {
		It("should not return error when it's not found", func() {
			Expect(deployWaiter.Destroy(ctx)).To(Succeed())
		})

		It("should not return error when it's deleted successfully", func() {
			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.Destroy(ctx)).To(Succeed())
		})

		It("should return error when it's not deleted successfully", func() {
			defer test.WithVars(
				&extensions.TimeNow, mockNow.Do,
				&gutil.TimeNow, mockNow.Do,
			)()

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()
			mc := mockclient.NewMockClient(ctrl)

			expected = empty.DeepCopy()
			expected.SetAnnotations(map[string]string{
				gutil.ConfirmationDeletion:         "true",
				v1beta1constants.GardenerTimestamp: now.UTC().String(),
			})

			// add deletion confirmation and timestamp annotation
			mc.EXPECT().Patch(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.Infrastructure{}), gomock.Any()).Return(nil)
			mc.EXPECT().Delete(ctx, expected).Times(1).Return(fakeErr)

			deployWaiter = infrastructure.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(deployWaiter.Destroy(ctx)).To(MatchError(fakeErr))
		})
	})

	Describe("#WaitCleanup", func() {
		It("should not return error when it's already removed", func() {
			Expect(deployWaiter.WaitCleanup(ctx)).To(Succeed())
		})

		It("should return error when it's not deleted successfully", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.WaitCleanup(ctx)).To(MatchError(ContainSubstring("Some error")))
		})
	})

	Describe("#Restore", func() {
		var (
			state      = &runtime.RawExtension{Raw: []byte(`{"dummy":"state"}`)}
			shootState = &gardencorev1alpha1.ShootState{
				Spec: gardencorev1alpha1.ShootStateSpec{
					Extensions: []gardencorev1alpha1.ExtensionResourceState{
						{
							Name:  pointer.StringPtr(name),
							Kind:  extensionsv1alpha1.InfrastructureResource,
							State: state,
						},
					},
				},
			}
		)

		It("should properly restore the infrastructure state if it exists", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			mc := mockclient.NewMockClient(ctrl)
			mc.EXPECT().Status().Return(mc)

			values.SSHPublicKey = sshPublicKey
			values.AnnotateOperation = true

			// deploy with wait-for-state annotation
			obj := expected.DeepCopy()
			metav1.SetMetaDataAnnotation(&obj.ObjectMeta, "gardener.cloud/operation", "wait-for-state")
			metav1.SetMetaDataAnnotation(&obj.ObjectMeta, "gardener.cloud/timestamp", now.UTC().String())
			obj.TypeMeta = metav1.TypeMeta{}
			test.EXPECTPatch(ctx, mc, obj, empty, types.MergePatchType)

			// restore state
			expectedWithState := obj.DeepCopy()
			expectedWithState.Status.State = state
			test.EXPECTPatch(ctx, mc, expectedWithState, obj, types.MergePatchType)

			// annotate with restore annotation
			expectedWithRestore := expectedWithState.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedWithRestore.ObjectMeta, "gardener.cloud/operation", "restore")
			test.EXPECTPatch(ctx, mc, expectedWithRestore, expectedWithState, types.MergePatchType)

			Expect(infrastructure.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond).Restore(ctx, shootState)).To(Succeed())
		})
	})

	Describe("#Migrate", func() {
		It("should migrate the resources", func() {
			defer test.WithVars(
				&infrastructure.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")

			deployWaiter.SetSSHPublicKey(sshPublicKey)
			Expect(deployWaiter.Migrate(ctx)).To(Succeed())

			actual := &extensionsv1alpha1.Infrastructure{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(expected), actual)).To(Succeed())
			expected.SetResourceVersion("2")
			metav1.SetMetaDataAnnotation(&expected.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationMigrate)
			metav1.SetMetaDataAnnotation(&expected.ObjectMeta, v1beta1constants.GardenerTimestamp, now.UTC().String())
			Expect(actual).To(DeepEqual(expected))
		})

		It("should not return error if resource does not exist", func() {
			Expect(deployWaiter.Migrate(ctx)).To(Succeed())
		})
	})

	Describe("#WaitMigrate", func() {
		It("should not return error when resource is missing", func() {
			Expect(deployWaiter.WaitMigrate(ctx)).To(Succeed())
		})

		It("should return error if resource is not yet migrated successfully", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateError,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.WaitMigrate(ctx)).To(MatchError(ContainSubstring("is not Migrate=Succeeded")))
		})

		It("should not return error if resource gets migrated successfully", func() {
			expected.Status.LastError = nil
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating infrastructure succeeds")
			Expect(deployWaiter.WaitMigrate(ctx)).To(Succeed(), "infrastructure is ready, should not return an error")
		})
	})

	Describe("#Get", func() {
		It("should return an error when the retrieval fails", func() {
			res, err := deployWaiter.Get(ctx)
			Expect(res).To(BeNil())
			Expect(err).To(MatchError(ContainSubstring("not found")))
		})

		It("should retrieve the object and extract the status", func() {
			Expect(deployWaiter.ProviderStatus()).To(BeNil())
			Expect(deployWaiter.NodesCIDR()).To(BeNil())

			var (
				providerStatus = &runtime.RawExtension{Raw: []byte(`{"some":"status"}`)}
				nodesCIDR      = pointer.StringPtr("1.2.3.4")
			)

			infra := empty.DeepCopy()
			infra.Status.ProviderStatus = providerStatus
			infra.Status.NodesCIDR = nodesCIDR
			Expect(c.Create(ctx, infra)).To(Succeed())

			expected = infra.DeepCopy()
			actual, err := deployWaiter.Get(ctx)
			Expect(err).NotTo(HaveOccurred())
			actual.SetGroupVersionKind(schema.GroupVersionKind{})
			Expect(actual).To(DeepEqual(expected))

			Expect(deployWaiter.ProviderStatus()).To(Equal(providerStatus))
			Expect(deployWaiter.NodesCIDR()).To(Equal(nodesCIDR))
		})
	})
})
