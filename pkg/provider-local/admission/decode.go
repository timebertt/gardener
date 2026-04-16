// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package admission

import (
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/gardener/gardener/pkg/provider-local/apis/local/v1alpha1"
)

// DecodeCloudProfileConfig decodes the given RawExtension into a CloudProfileConfig.
func DecodeCloudProfileConfig(decoder runtime.Decoder, config *runtime.RawExtension) (*v1alpha1.CloudProfileConfig, error) {
	cloudProfileConfig := &v1alpha1.CloudProfileConfig{}
	if _, _, err := decoder.Decode(config.Raw, nil, cloudProfileConfig); err != nil {
		return nil, err
	}
	return cloudProfileConfig, nil
}
