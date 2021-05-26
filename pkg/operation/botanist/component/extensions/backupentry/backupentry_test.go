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

package backupentry_test

import (
	"context"
	"fmt"
	"time"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/logger"
	mockclient "github.com/gardener/gardener/pkg/mock/controller-runtime/client"
	mocktime "github.com/gardener/gardener/pkg/mock/go/time"
	"github.com/gardener/gardener/pkg/operation/botanist/component"
	"github.com/gardener/gardener/pkg/operation/botanist/component/extensions/backupentry"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("#BackupEntry", func() {
	var (
		ctrl *gomock.Controller

		ctx              context.Context
		c                client.Client
		empty            *extensionsv1alpha1.BackupEntry
		expected         *extensionsv1alpha1.BackupEntry
		values           *backupentry.Values
		log              logrus.FieldLogger
		fakeErr          error
		defaultDepWaiter component.DeployMigrateWaiter

		mockNow *mocktime.MockNow
		now     time.Time

		name                       = "test-deploy"
		region                     = "region"
		bucketName                 = "bucketname"
		providerType               = "foo"
		providerConfig             = &runtime.RawExtension{Raw: []byte(`{"bar":"foo"}`)}
		backupBucketProviderStatus = &runtime.RawExtension{Raw: []byte(`{"foo":"bar"}`)}
		secretRef                  = corev1.SecretReference{
			Name:      "secretname",
			Namespace: "secretnamespace",
		}
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())

		mockNow = mocktime.NewMockNow(ctrl)
		now = time.Now()

		ctx = context.TODO()
		log = logger.NewNopLogger()
		fakeErr = fmt.Errorf("some random error")

		s := runtime.NewScheme()
		Expect(extensionsv1alpha1.AddToScheme(s)).To(Succeed())

		c = fake.NewFakeClientWithScheme(s)

		values = &backupentry.Values{
			Name:                       name,
			Type:                       providerType,
			ProviderConfig:             providerConfig,
			Region:                     region,
			SecretRef:                  secretRef,
			BucketName:                 bucketName,
			BackupBucketProviderStatus: backupBucketProviderStatus,
		}

		empty = &extensionsv1alpha1.BackupEntry{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}

		expected = &extensionsv1alpha1.BackupEntry{
			TypeMeta: metav1.TypeMeta{
				APIVersion: extensionsv1alpha1.SchemeGroupVersion.String(),
				Kind:       extensionsv1alpha1.BackupEntryResource,
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
					v1beta1constants.GardenerTimestamp: now.UTC().String(),
				},
			},
			Spec: extensionsv1alpha1.BackupEntrySpec{
				DefaultSpec: extensionsv1alpha1.DefaultSpec{
					Type:           providerType,
					ProviderConfig: providerConfig,
				},
				Region:                     region,
				SecretRef:                  secretRef,
				BucketName:                 bucketName,
				BackupBucketProviderStatus: backupBucketProviderStatus,
			},
		}

		defaultDepWaiter = backupentry.New(log, c, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("#Deploy", func() {
		BeforeEach(func() {
			expected.ResourceVersion = "1"
		})

		It("should create correct BackupEntry", func() {
			defer test.WithVars(&backupentry.TimeNow, mockNow.Do)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

			actual := &extensionsv1alpha1.BackupEntry{}
			Expect(c.Get(ctx, client.ObjectKey{Name: name}, actual)).To(Succeed())

			Expect(actual).To(DeepEqual(expected))
		})
	})

	Describe("#Wait", func() {
		It("should return error when it's not found", func() {
			Expect(defaultDepWaiter.Wait(ctx)).To(MatchError(ContainSubstring("not found")))
		})

		It("should return error when it's not ready", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating backupentry succeeds")
			Expect(defaultDepWaiter.Wait(ctx)).To(MatchError(ContainSubstring("error during reconciliation: Some error")), "backupentry indicates error")
		})

		It("should return error if we haven't observed the latest timestamp annotation", func() {
			defer test.WithVars(
				&backupentry.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			By("deploy")
			// Deploy should fill internal state with the added timestamp annotation
			Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

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
			Expect(c.Patch(ctx, expected, patch)).To(Succeed(), "patching backupentry succeeds")

			By("wait")
			Expect(defaultDepWaiter.Wait(ctx)).NotTo(Succeed(), "backupentry indicates error")
		})

		It("should return no error when it's ready", func() {
			defer test.WithVars(
				&backupentry.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			By("deploy")
			// Deploy should fill internal state with the added timestamp annotation
			Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

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
			Expect(c.Patch(ctx, expected, patch)).To(Succeed(), "patching backupentry succeeds")

			By("wait")
			Expect(defaultDepWaiter.Wait(ctx)).To(Succeed(), "backupentry is ready")
		})
	})

	Describe("#Destroy", func() {
		It("should not return error when it's not found", func() {
			Expect(defaultDepWaiter.Destroy(ctx)).To(Succeed())
		})

		It("should not return error when it's deleted successfully", func() {
			Expect(c.Create(ctx, expected)).To(Succeed(), "adding pre-existing backupentry succeeds")
			Expect(defaultDepWaiter.Destroy(ctx)).To(Succeed())
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
			mc.EXPECT().Patch(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.BackupEntry{}), gomock.Any()).Return(nil)
			mc.EXPECT().Delete(ctx, expected).Times(1).Return(fakeErr)

			defaultDepWaiter = backupentry.New(log, mc, &backupentry.Values{Name: name}, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(defaultDepWaiter.Destroy(ctx)).To(MatchError(fakeErr))
		})
	})

	Describe("#WaitCleanup", func() {
		It("should not return error when it's already removed", func() {
			Expect(defaultDepWaiter.WaitCleanup(ctx)).To(Succeed())
		})

		It("should return error when it's not deleted successfully", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating backupentry succeeds")
			Expect(defaultDepWaiter.WaitCleanup(ctx)).To(MatchError(ContainSubstring("Some error")))
		})
	})

	Describe("#Restore", func() {
		var (
			state      = &runtime.RawExtension{Raw: []byte(`{"dummy":"state"}`)}
			shootState *gardencorev1alpha1.ShootState
		)

		BeforeEach(func() {
			shootState = &gardencorev1alpha1.ShootState{
				Spec: gardencorev1alpha1.ShootStateSpec{
					Extensions: []gardencorev1alpha1.ExtensionResourceState{
						{
							Name:  &expected.Name,
							Kind:  extensionsv1alpha1.BackupEntryResource,
							State: state,
						},
					},
				},
			}
		})

		It("should properly restore the BackupEntry state if it exists", func() {
			// NB(timebertt): such tests with mocks are ridiculously hard to adapt to refactoring changes.
			// Let's **please** just stop writing such tests with mocks and use a fake client or envtest instead.
			// Testing with mocks does not only assert that the tested unit fulfills its task but also
			// asserts that specific calls are made in order to fulfill its task. However, we/the caller don't
			// care about what helper funcs are used internally or whether it uses update or patch to fullfill
			// the task, as long as the result is what we expect (which is what should be asserted instead).
			defer test.WithVars(
				&backupentry.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			mc := mockclient.NewMockClient(ctrl)
			mc.EXPECT().Status().Return(mc)

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

			Expect(backupentry.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond).Restore(ctx, shootState)).To(Succeed())
		})
	})

	Describe("#Migrate", func() {
		It("should migrate the resource", func() {
			defer test.WithVars(
				&backupentry.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()
			mc := mockclient.NewMockClient(ctrl)

			expectedCopy := empty.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationMigrate)
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerTimestamp, now.UTC().String())
			test.EXPECTPatch(ctx, mc, expectedCopy, empty, types.MergePatchType)

			defaultDepWaiter = backupentry.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(defaultDepWaiter.Migrate(ctx)).To(Succeed())
		})

		It("should not return error if resource does not exist", func() {
			defer test.WithVars(
				&backupentry.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()
			mc := mockclient.NewMockClient(ctrl)

			expectedCopy := empty.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationMigrate)
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerTimestamp, now.UTC().String())
			test.EXPECTPatch(ctx, mc, expectedCopy, empty, types.MergePatchType)

			defaultDepWaiter = backupentry.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(defaultDepWaiter.Migrate(ctx)).To(Succeed())
		})
	})

	Describe("#WaitMigrate", func() {
		It("should not return error when resource is missing", func() {
			Expect(defaultDepWaiter.WaitMigrate(ctx)).To(Succeed())
		})

		It("should return error if resource is not yet migrated successfully", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateError,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating BackupEntry succeeds")
			Expect(defaultDepWaiter.WaitMigrate(ctx)).To(MatchError(ContainSubstring("is not Migrate=Succeeded")))
		})

		It("should not return error if resource gets migrated successfully", func() {
			expected.Status.LastError = nil
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating BackupEntry succeeds")
			Expect(defaultDepWaiter.WaitMigrate(ctx)).To(Succeed(), "BackupEntry is ready, should not return an error")
		})
	})
})
