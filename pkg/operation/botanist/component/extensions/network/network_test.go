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

package network_test

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/logger"
	mockclient "github.com/gardener/gardener/pkg/mock/controller-runtime/client"
	mocktime "github.com/gardener/gardener/pkg/mock/go/time"
	"github.com/gardener/gardener/pkg/operation/botanist/component"
	"github.com/gardener/gardener/pkg/operation/botanist/component/extensions/network"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"
)

var _ = Describe("#Network", func() {
	const (
		networkNs          = "test-namespace"
		networkName        = "test-deploy"
		networkType        = "calico"
		networkPodIp       = "100.96.0.0"
		networkPodMask     = 11
		networkServiceIp   = "100.64.0.0"
		networkServiceMask = 13
	)
	var (
		ctrl *gomock.Controller

		ctx              context.Context
		c                client.Client
		expected, empty  *extensionsv1alpha1.Network
		values           *network.Values
		log              logrus.FieldLogger
		defaultDepWaiter component.DeployMigrateWaiter

		mockNow *mocktime.MockNow
		now     time.Time

		networkPodCIDR     = fmt.Sprintf("%s/%d", networkPodIp, networkPodMask)
		networkServiceCIDR = fmt.Sprintf("%s/%d", networkServiceIp, networkServiceMask)
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())

		mockNow = mocktime.NewMockNow(ctrl)

		ctx = context.TODO()
		log = logger.NewNopLogger()

		s := runtime.NewScheme()
		Expect(extensionsv1alpha1.AddToScheme(s)).NotTo(HaveOccurred())

		c = fake.NewFakeClientWithScheme(s)

		podCIDR := net.IPNet{
			IP:   net.ParseIP(networkPodIp),
			Mask: net.CIDRMask(networkPodMask, 32),
		}
		serviceCIDR := net.IPNet{
			IP:   net.ParseIP(networkServiceIp),
			Mask: net.CIDRMask(networkServiceMask, 32),
		}

		values = &network.Values{
			Name:           networkName,
			Namespace:      networkNs,
			Type:           networkType,
			ProviderConfig: nil,
			PodCIDR:        &podCIDR,
			ServiceCIDR:    &serviceCIDR,
		}

		empty = &extensionsv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Name:      networkName,
				Namespace: networkNs,
			},
		}
		expected = &extensionsv1alpha1.Network{
			ObjectMeta: metav1.ObjectMeta{
				Name:      networkName,
				Namespace: networkNs,
				Annotations: map[string]string{
					v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
					v1beta1constants.GardenerTimestamp: now.UTC().String(),
				},
			},
			Spec: extensionsv1alpha1.NetworkSpec{
				DefaultSpec: extensionsv1alpha1.DefaultSpec{
					Type:           networkType,
					ProviderConfig: nil,
				},
				PodCIDR:     networkPodCIDR,
				ServiceCIDR: networkServiceCIDR,
			},
		}

		defaultDepWaiter = network.New(log, c, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
	})

	AfterEach(func() {
		ctrl.Finish()
	})

	Describe("#Deploy", func() {
		It("should create correct Network", func() {
			defer test.WithVars(
				&network.TimeNow, mockNow.Do,
			)()

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			Expect(defaultDepWaiter.Deploy(ctx)).ToNot(HaveOccurred())

			actual := &extensionsv1alpha1.Network{}
			err := c.Get(ctx, client.ObjectKey{Name: networkName, Namespace: networkNs}, actual)

			Expect(err).NotTo(HaveOccurred())
			Expect(actual).To(DeepDerivativeEqual(expected))
		})
	})

	Describe("#Wait", func() {
		It("should return error when it's not found", func() {
			Expect(defaultDepWaiter.Wait(ctx)).To(HaveOccurred())
		})

		It("should return error when it's not ready", func() {
			expected.Status.LastError = &gardencorev1beta1.LastError{
				Description: "Some error",
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating network succeeds")
			Expect(defaultDepWaiter.Wait(ctx)).To(HaveOccurred(), "network indicates error")
		})

		It("should return no error when is ready", func() {
			expected.Status.LastError = nil
			// remove operation annotation
			expected.ObjectMeta.Annotations = map[string]string{}
			// set last operation
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating network succeeds")
			Expect(defaultDepWaiter.Wait(ctx)).ToNot(HaveOccurred(), "network is ready, should not return an error")
		})
	})

	Describe("#Destroy", func() {
		It("should not return error when it's not found", func() {
			Expect(defaultDepWaiter.Destroy(ctx)).ToNot(HaveOccurred())
		})

		It("should not return error when it's deleted successfully", func() {
			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "adding pre-existing network succeeds")

			Expect(defaultDepWaiter.Destroy(ctx)).ToNot(HaveOccurred())
		})

		It("should return error when it's not deleted successfully", func() {
			defer test.WithVars(
				&extensions.TimeNow, mockNow.Do,
				&gutil.TimeNow, mockNow.Do,
			)()

			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

			expected := extensionsv1alpha1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name:      networkName,
					Namespace: networkNs,
					Annotations: map[string]string{
						gutil.ConfirmationDeletion:         "true",
						v1beta1constants.GardenerTimestamp: now.UTC().String(),
					},
				}}

			mc := mockclient.NewMockClient(ctrl)
			// add deletion confirmation and Timestamp annotation
			mc.EXPECT().Patch(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.Network{}), gomock.Any())

			mc.EXPECT().Delete(ctx, &expected).Times(1).Return(fmt.Errorf("some random error"))

			defaultDepWaiter = network.New(log, mc, &network.Values{
				Namespace: networkNs,
				Name:      networkName,
			}, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)

			err := defaultDepWaiter.Destroy(ctx)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("#WaitCleanup", func() {
		It("should not return error when it's already removed", func() {
			Expect(defaultDepWaiter.WaitCleanup(ctx)).ToNot(HaveOccurred())
		})
	})

	Describe("#Restore", func() {
		var (
			shootState *gardencorev1alpha1.ShootState
		)

		BeforeEach(func() {
			shootState = &gardencorev1alpha1.ShootState{
				Spec: gardencorev1alpha1.ShootStateSpec{
					Extensions: []gardencorev1alpha1.ExtensionResourceState{
						{
							Name:  &expected.Name,
							Kind:  extensionsv1alpha1.NetworkResource,
							State: &runtime.RawExtension{Raw: []byte(`{"dummy":"state"}`)},
						},
					},
				},
			}
		})

		It("should restore the network state if it exists in the shoot state", func() {
			defer test.WithVars(
				&network.TimeNow, mockNow.Do,
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
			expectedWithState.Status.State = &runtime.RawExtension{Raw: []byte(`{"dummy":"state"}`)}
			test.EXPECTPatch(ctx, mc, expectedWithState, obj, types.MergePatchType)

			// annotate with restore annotation
			expectedWithRestore := expectedWithState.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedWithRestore.ObjectMeta, "gardener.cloud/operation", "restore")
			test.EXPECTPatch(ctx, mc, expectedWithRestore, expectedWithState, types.MergePatchType)

			defaultDepWaiter = network.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(defaultDepWaiter.Restore(ctx, shootState)).To(Succeed())
		})
	})

	Describe("#Migrate", func() {
		It("should migrate the resource", func() {
			defer test.WithVars(
				&network.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()
			mc := mockclient.NewMockClient(ctrl)

			expectedCopy := empty.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationMigrate)
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerTimestamp, now.UTC().String())
			test.EXPECTPatch(ctx, mc, expectedCopy, empty, types.MergePatchType)

			defaultDepWaiter = network.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
			Expect(defaultDepWaiter.Migrate(ctx)).To(Succeed())
		})

		It("should not return error if resource does not exist", func() {
			defer test.WithVars(
				&network.TimeNow, mockNow.Do,
				&extensions.TimeNow, mockNow.Do,
			)()
			mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()
			mc := mockclient.NewMockClient(ctrl)

			expectedCopy := empty.DeepCopy()
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerOperation, v1beta1constants.GardenerOperationMigrate)
			metav1.SetMetaDataAnnotation(&expectedCopy.ObjectMeta, v1beta1constants.GardenerTimestamp, now.UTC().String())
			test.EXPECTPatch(ctx, mc, expectedCopy, empty, types.MergePatchType)

			defaultDepWaiter = network.New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
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

			Expect(c.Create(ctx, expected)).To(Succeed(), "creating network succeeds")
			Expect(defaultDepWaiter.WaitMigrate(ctx)).To(HaveOccurred())
		})

		It("should not return error if resource gets migrated successfully", func() {
			expected.Status.LastError = nil
			expected.Status.LastOperation = &gardencorev1beta1.LastOperation{
				State: gardencorev1beta1.LastOperationStateSucceeded,
				Type:  gardencorev1beta1.LastOperationTypeMigrate,
			}

			Expect(c.Create(ctx, expected)).ToNot(HaveOccurred(), "creating network succeeds")
			Expect(defaultDepWaiter.WaitMigrate(ctx)).ToNot(HaveOccurred(), "network is ready, should not return an error")
		})
	})
})
