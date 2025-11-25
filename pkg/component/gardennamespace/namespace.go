// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardennamespace

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	podsecurityadmissionapi "k8s.io/pod-security-admission/api"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/controllerutils"
)

// ReconcileGardenNamespace ensures that the Garden namespace exists with the appropriate labels and annotations.
func ReconcileGardenNamespace(ctx context.Context, client client.Client, namespaceName string, zones []string) error {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
	_, err := controllerutils.CreateOrGetAndMergePatch(ctx, client, namespace, func() error {
		metav1.SetMetaDataLabel(&namespace.ObjectMeta, podsecurityadmissionapi.EnforceLevelLabel, string(podsecurityadmissionapi.LevelPrivileged))
		metav1.SetMetaDataLabel(&namespace.ObjectMeta, resourcesv1alpha1.HighAvailabilityConfigConsider, "true")
		metav1.SetMetaDataLabel(&namespace.ObjectMeta, v1beta1constants.GardenRole, v1beta1constants.GardenRoleGarden)
		metav1.SetMetaDataAnnotation(&namespace.ObjectMeta, resourcesv1alpha1.HighAvailabilityConfigZones, strings.Join(zones, ","))
		return nil
	})
	return err
}
