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

package extensions_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	. "github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/logger"
	mockclient "github.com/gardener/gardener/pkg/mock/controller-runtime/client"
	mocktime "github.com/gardener/gardener/pkg/mock/go/time"
	"github.com/gardener/gardener/pkg/utils/test"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/types"
)

var _ = Describe("extensions", func() {
	var (
		ctx     context.Context
		log     logrus.FieldLogger
		ctrl    *gomock.Controller
		mockNow *mocktime.MockNow
		now     time.Time

		c client.Client

		defaultInterval  time.Duration
		defaultTimeout   time.Duration
		defaultThreshold time.Duration

		namespace string
		name      string

		expected *extensionsv1alpha1.Worker
	)

	BeforeEach(func() {
		ctx = context.TODO()
		log = logger.NewNopLogger()
		ctrl = gomock.NewController(GinkgoT())
		mockNow = mocktime.NewMockNow(ctrl)

		s := runtime.NewScheme()
		Expect(extensionsv1alpha1.AddToScheme(s)).NotTo(HaveOccurred())
		c = fake.NewFakeClientWithScheme(s)

		defaultInterval = 1 * time.Millisecond
		defaultTimeout = 1 * time.Millisecond
		defaultThreshold = 1 * time.Millisecond

		namespace = "test-namespace"
		name = "test-name"

		expected = &extensionsv1alpha1.Worker{
			TypeMeta: metav1.TypeMeta{
				Kind:       extensionsv1alpha1.WorkerResource,
				APIVersion: extensionsv1alpha1.SchemeGroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
		}
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("#WaitUntilExtensionObjectReady", func() {
		It("should return error if extension object does not exist", func() {
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultTimeout, defaultTimeout, nil,
			)
			Expect(err).To(HaveOccurred())
		})

		It("should return error if extension object is not ready", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, nil,
			)
			Expect(err).To(HaveOccurred(), "worker readiness error")
		})

		It("should return success if extension object got ready the first time", func() {
			passedObj := expected.DeepCopy()
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				LastUpdateTime: metav1.Now(),
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				passedObj, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, nil,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return error if extension object's last operation has not been updated", func() {
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				LastUpdateTime: metav1.Now(),
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, nil,
			)
			Expect(err).To(MatchError(ContainSubstring("operation has not been updated")), "worker readiness error")
		})

		It("should return success if extension object got ready again", func() {
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				LastUpdateTime: metav1.Now(),
			}
			passedObj := expected.DeepCopy()
			updatedTime := expected.Status.LastOperation.LastUpdateTime.Add(time.Second)
			expected.Status.LastOperation.LastUpdateTime = metav1.NewTime(updatedTime)

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				passedObj, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, nil,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should call postReadyFunc if extension object is ready", func() {
			passedObj := expected.DeepCopy()
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				LastUpdateTime: metav1.Now(),
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")

			val := 0
			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				passedObj, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, func() error {
					val++
					return nil
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1))
		})

		It("should set passed object to latest state once ready", func() {
			passedObj := expected.DeepCopy()
			passedObj.SetAnnotations(map[string]string{"gardener.cloud/operation": "reconcile"})
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State:          gardencorev1beta1.LastOperationStateSucceeded,
				LastUpdateTime: metav1.Now().Rfc3339Copy(), // time.Now returns millisecond precision which is not marshalled
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")

			err := WaitUntilExtensionObjectReady(
				ctx, c, log,
				passedObj, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, nil,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(passedObj).To(Equal(expected))
		})
	})

	Describe("#WaitUntilObjectReadyWithHealthFunction", func() {
		It("should return error if object does not exist error", func() {
			err := WaitUntilObjectReadyWithHealthFunction(
				ctx, c, log,
				func(obj client.Object) error {
					return nil
				},
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout,
				nil,
			)
			Expect(err).To(HaveOccurred())
		})

		It("should return error if ready func returns error", func() {
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilObjectReadyWithHealthFunction(
				ctx, c, log,
				func(obj client.Object) error {
					return errors.New("error")
				},
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout,
				nil,
			)
			Expect(err).To(HaveOccurred())
		})

		It("should return success if health func does not return error", func() {
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilObjectReadyWithHealthFunction(
				ctx, c, log,
				func(obj client.Object) error {
					return nil
				},
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout,
				nil,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should pass correct object to health func", func() {
			passedObj := expected.DeepCopy()
			metav1.SetMetaDataAnnotation(&passedObj.ObjectMeta, "foo", "bar")
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")
			err := WaitUntilObjectReadyWithHealthFunction(
				ctx, c, log,
				func(obj client.Object) error {
					Expect(obj).To(Equal(expected))
					return nil
				},
				passedObj, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout,
				nil,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should call post ready func if health func does not return error", func() {
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating worker succeeds")

			val := 0
			err := WaitUntilObjectReadyWithHealthFunction(
				ctx, c, log,
				func(obj client.Object) error {
					return nil
				},
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultThreshold, defaultTimeout, func() error {
					val++
					return nil
				},
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(val).To(Equal(1))
		})
	})

	Describe("#DeleteExtensionObject", func() {
		It("should not return error if extension object does not exist", func() {
			Expect(DeleteExtensionObject(ctx, c, expected)).To(Succeed())
		})

		It("should not return error if deleted successfully", func() {
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
			Expect(DeleteExtensionObject(ctx, c, expected)).To(Succeed())
		})

		It("should delete extension object", func() {
			defer test.WithVars(
				&TimeNow, mockNow.Do,
			)()

			expected.Annotations = map[string]string{
				v1beta1constants.GardenerTimestamp: now.UTC().String(),
			}

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			mc := mockclient.NewMockClient(ctrl)
			// add deletion confirmation and Timestamp annotation
			mc.EXPECT().Patch(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.Worker{}), gomock.Any()).SetArg(1, *expected).Return(nil)
			mc.EXPECT().Delete(ctx, expected).Times(1).Return(fmt.Errorf("some random error"))

			Expect(DeleteExtensionObject(ctx, mc, expected)).To(HaveOccurred())
		})
	})

	Describe("#DeleteExtensionObjects", func() {
		It("should delete all extension objects", func() {
			deletionTimestamp := metav1.Now()
			expected.ObjectMeta.DeletionTimestamp = &deletionTimestamp

			expected2 := expected.DeepCopy()
			expected2.Name = "worker2"
			list := &extensionsv1alpha1.WorkerList{
				Items: []extensionsv1alpha1.Worker{*expected, *expected2},
			}
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
			Expect(c.Create(ctx, expected2)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")

			err := DeleteExtensionObjects(
				ctx,
				c,
				list,
				namespace,
				func(obj extensionsv1alpha1.Object) bool { return true },
			)

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("#WaitUntilExtensionObjectsDeleted", func() {
		It("should return error if atleast one extension object is not deleted", func() {
			list := &extensionsv1alpha1.WorkerList{}

			deletionTimestamp := metav1.Now()
			expected.ObjectMeta.DeletionTimestamp = &deletionTimestamp

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")

			err := WaitUntilExtensionObjectsDeleted(
				ctx,
				c,
				log,
				list,
				extensionsv1alpha1.WorkerResource,
				namespace, defaultInterval, defaultTimeout,
				func(object extensionsv1alpha1.Object) bool { return true })

			Expect(err).To(HaveOccurred())
		})

		It("should return success if all extensions CRs are deleted", func() {
			list := &extensionsv1alpha1.WorkerList{}
			err := WaitUntilExtensionObjectsDeleted(
				ctx,
				c,
				log,
				list,
				extensionsv1alpha1.WorkerResource,
				namespace, defaultInterval, defaultTimeout,
				func(object extensionsv1alpha1.Object) bool { return true })
			Expect(err).NotTo(HaveOccurred())
		})

	})

	Describe("#WaitUntilExtensionObjectDeleted", func() {
		It("should return error if extension object is not deleted", func() {
			deletionTimestamp := metav1.Now()
			expected.ObjectMeta.DeletionTimestamp = &deletionTimestamp

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
			err := WaitUntilExtensionObjectDeleted(ctx, c, log,
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultTimeout)

			Expect(err).To(HaveOccurred())
		})

		It("should return success if extensions CRs gets deleted", func() {
			err := WaitUntilExtensionObjectDeleted(ctx, c, log,
				expected, extensionsv1alpha1.WorkerResource,
				defaultInterval, defaultTimeout)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("restoring extension object state", func() {
		var (
			expectedState *runtime.RawExtension
			shootState    *gardencorev1alpha1.ShootState
		)

		BeforeEach(func() {
			expectedState = &runtime.RawExtension{Raw: []byte(`{"data":"value"}`)}

			shootState = &gardencorev1alpha1.ShootState{
				Spec: gardencorev1alpha1.ShootStateSpec{
					Extensions: []gardencorev1alpha1.ExtensionResourceState{
						{
							Name:  &name,
							Kind:  extensionsv1alpha1.WorkerResource,
							State: expectedState,
						},
					},
				},
			}
		})

		Describe("#RestoreExtensionWithDeployFunction", func() {
			It("should restore the extension object state with the provided deploy fuction and annotate it for restoration", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				err := RestoreExtensionWithDeployFunction(
					ctx,
					c,
					shootState,
					extensionsv1alpha1.WorkerResource,
					func(ctx context.Context, operationAnnotation string) (extensionsv1alpha1.Object, error) {
						Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
						return expected, nil
					},
				)

				Expect(err).NotTo(HaveOccurred())
				Expect(expected.Annotations).To(Equal(map[string]string{
					v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationRestore,
					v1beta1constants.GardenerTimestamp: now.UTC().String(),
				}))
				Expect(expected.Status.State).To(Equal(expectedState))
			})

			It("should only annotate the resource with restore operation annotation if a corresponding state does not exist in the ShootState", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				expected.Name = "worker2"
				err := RestoreExtensionWithDeployFunction(
					ctx,
					c,
					shootState,
					extensionsv1alpha1.WorkerResource,
					func(ctx context.Context, operationAnnotation string) (extensionsv1alpha1.Object, error) {
						Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
						return expected, nil
					},
				)

				Expect(err).NotTo(HaveOccurred())
				Expect(expected.Annotations).To(Equal(map[string]string{
					v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationRestore,
					v1beta1constants.GardenerTimestamp: now.UTC().String(),
				}))
				Expect(expected.Status.State).To(BeNil())
			})
		})

		Describe("#RestoreExtensionObjectState", func() {
			It("should return error if the extension object does not exist", func() {
				err := RestoreExtensionObjectState(
					ctx,
					c,
					shootState,
					expected,
					extensionsv1alpha1.WorkerResource,
				)
				Expect(err).To(HaveOccurred())
			})

			It("should update the state if the extension object exists", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
				err := RestoreExtensionObjectState(
					ctx,
					c,
					shootState,
					expected,
					extensionsv1alpha1.WorkerResource,
				)
				Expect(err).NotTo(HaveOccurred())
				Expect(expected.Status.State).To(Equal(expectedState))
			})
		})
	})

	Describe("#MigrateExtensionObject", func() {
		It("should not return error if extension object does not exist", func() {
			Expect(MigrateExtensionObject(ctx, c, expected)).To(Succeed())
		})

		It("should properly annotate extension object for migration", func() {
			defer test.WithVars(
				&TimeNow, mockNow.Do,
			)()

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			expectedWithAnnotations := expected.DeepCopy()
			expectedWithAnnotations.Annotations = map[string]string{
				v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationMigrate,
				v1beta1constants.GardenerTimestamp: now.UTC().String(),
			}

			mc := mockclient.NewMockClient(ctrl)
			mc.EXPECT().Patch(ctx, expectedWithAnnotations, gomock.AssignableToTypeOf(client.MergeFrom(expected))).Return(nil)

			err := MigrateExtensionObject(ctx, mc, expected)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("#MigrateExtensionObjects", func() {
		It("should not return error if there are no extension resources", func() {
			Expect(
				MigrateExtensionObjects(ctx, c, &extensionsv1alpha1.BackupBucketList{}, namespace),
			).To(Succeed())
		})

		It("should properly annotate all extension objects for migration", func() {
			for i := 0; i < 4; i++ {
				containerRuntimeExtension := &extensionsv1alpha1.ContainerRuntime{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: namespace,
						Name:      fmt.Sprintf("containerruntime-%d", i),
					},
				}
				Expect(c.Create(ctx, containerRuntimeExtension)).To(Succeed())
			}

			Expect(
				MigrateExtensionObjects(ctx, c, &extensionsv1alpha1.ContainerRuntimeList{}, namespace),
			).To(Succeed())

			containerRuntimeList := &extensionsv1alpha1.ContainerRuntimeList{}
			Expect(c.List(ctx, containerRuntimeList, client.InNamespace(namespace))).To(Succeed())
			Expect(len(containerRuntimeList.Items)).To(Equal(4))
			for _, item := range containerRuntimeList.Items {
				Expect(item.Annotations[v1beta1constants.GardenerOperation]).To(Equal(v1beta1constants.GardenerOperationMigrate))
			}
		})
	})

	Describe("#WaitUntilExtensionObjectMigrated", func() {
		It("should not return error if resource does not exist", func() {
			err := WaitUntilExtensionObjectMigrated(
				ctx,
				c,
				expected,
				defaultInterval, defaultTimeout,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should return error if migration times out",
			func(lastOperation *gardencorev1beta1.LastOperation, match func() GomegaMatcher) {
				expected.Status.LastOperation = lastOperation
				Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
				err := WaitUntilExtensionObjectMigrated(
					ctx,
					c,
					expected,
					defaultInterval, defaultTimeout,
				)
				Expect(err).To(match())
			},
			Entry("last operation is not Migrate", &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
				Type:  gardencorev1beta1.LastOperationTypeReconcile,
			}, HaveOccurred),
			Entry("last operation is Migrate but not successful", &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateProcessing,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}, HaveOccurred),
			Entry("last operation is Migrate and successful", &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}, Succeed),
		)
	})

	Describe("#WaitUntilExtensionObjectsMigrated", func() {
		It("should not return error if there are no extension objects", func() {
			Expect(WaitUntilExtensionObjectsMigrated(
				ctx,
				c,
				&extensionsv1alpha1.WorkerList{},

				namespace,
				defaultInterval,
				defaultTimeout)).To(Succeed())
		})

		DescribeTable("should return error if migration times out",
			func(lastOperations []*gardencorev1beta1.LastOperation, match func() GomegaMatcher) {
				for i, lastOp := range lastOperations {
					existing := expected.DeepCopy()
					existing.Status.LastOperation = lastOp
					existing.Name = fmt.Sprintf("worker-%d", i)
					Expect(c.Create(ctx, existing)).ToNot(HaveOccurred(), "adding pre-existing worker succeeds")
				}
				err := WaitUntilExtensionObjectsMigrated(
					ctx,
					c,
					&extensionsv1alpha1.WorkerList{},
					namespace,
					defaultInterval,
					defaultTimeout)
				Expect(err).To(match())
			},
			Entry("last operation is not Migrate",
				[]*gardencorev1beta1.LastOperation{
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeReconcile,
					},
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeReconcile,
					},
				},
				HaveOccurred),
			Entry("last operation is Migrate but not successful",
				[]*gardencorev1beta1.LastOperation{
					{
						State: gardencorev1beta1.LastOperationStateProcessing,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					},
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeReconcile,
					},
				}, HaveOccurred),
			Entry("last operation is Migrate and successful on only one resource",
				[]*gardencorev1beta1.LastOperation{
					{
						State: gardencorev1beta1.LastOperationStateProcessing,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					},
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					},
				}, HaveOccurred),
			Entry("last operation is Migrate and successful on all resources",
				[]*gardencorev1beta1.LastOperation{
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					},
					{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					},
				}, Succeed),
		)
	})

	Describe("#AnnotateObjectWithOperation", func() {
		It("should return error if object does not exist", func() {
			Expect(AnnotateObjectWithOperation(ctx, c, expected, v1beta1constants.GardenerOperationMigrate)).NotTo(Succeed())
		})

		It("should annotate extension object with operation", func() {
			defer test.WithVars(
				&TimeNow, mockNow.Do,
			)()

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			expectedWithAnnotations := expected.DeepCopy()
			expectedWithAnnotations.Annotations = map[string]string{
				v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationMigrate,
				v1beta1constants.GardenerTimestamp: now.UTC().String(),
			}

			mc := mockclient.NewMockClient(ctrl)
			mc.EXPECT().Patch(ctx, expectedWithAnnotations, gomock.AssignableToTypeOf(client.MergeFrom(expected))).Return(nil)

			err := AnnotateObjectWithOperation(ctx, mc, expected, v1beta1constants.GardenerOperationMigrate)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
