// Copyright 2023 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package validation

import (
	"encoding/json"
	"reflect"

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/gardener/gardener/pkg/apis/core"
)

// ValidateSecretName can be used to check whether the given secret name is valid.
// Prefix indicates this name will be used as part of generation, in which case
// trailing dashes are allowed.
var ValidateSecretName = apivalidation.NameIsDNSSubdomain

// ValidateSecret tests if required fields in the Secret are set.
func ValidateSecret(secret *core.InternalSecret) field.ErrorList {
	allErrs := apivalidation.ValidateObjectMeta(&secret.ObjectMeta, true, ValidateSecretName, field.NewPath("metadata"))

	dataPath := field.NewPath("data")
	totalSize := 0
	for key, value := range secret.Data {
		for _, msg := range validation.IsConfigMapKey(key) {
			allErrs = append(allErrs, field.Invalid(dataPath.Key(key), key, msg))
		}
		totalSize += len(value)
	}
	if totalSize > core.MaxSecretSize {
		allErrs = append(allErrs, field.TooLong(dataPath, "", core.MaxSecretSize))
	}

	switch secret.Type {
	case core.SecretTypeServiceAccountToken:
		// Only require Annotations[kubernetes.io/service-account.name]
		// Additional fields (like Annotations[kubernetes.io/service-account.uid] and Data[token]) might be contributed later by a controller loop
		if value := secret.Annotations[core.ServiceAccountNameKey]; len(value) == 0 {
			allErrs = append(allErrs, field.Required(field.NewPath("metadata", "annotations").Key(core.ServiceAccountNameKey), ""))
		}
	case core.SecretTypeOpaque, "":
	// no-op
	case core.SecretTypeDockercfg:
		dockercfgBytes, exists := secret.Data[core.DockerConfigKey]
		if !exists {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.DockerConfigKey), ""))
			break
		}

		// make sure that the content is well-formed json.
		if err := json.Unmarshal(dockercfgBytes, &map[string]interface{}{}); err != nil {
			allErrs = append(allErrs, field.Invalid(dataPath.Key(core.DockerConfigKey), "<secret contents redacted>", err.Error()))
		}
	case core.SecretTypeDockerConfigJSON:
		dockerConfigJSONBytes, exists := secret.Data[core.DockerConfigJSONKey]
		if !exists {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.DockerConfigJSONKey), ""))
			break
		}

		// make sure that the content is well-formed json.
		if err := json.Unmarshal(dockerConfigJSONBytes, &map[string]interface{}{}); err != nil {
			allErrs = append(allErrs, field.Invalid(dataPath.Key(core.DockerConfigJSONKey), "<secret contents redacted>", err.Error()))
		}
	case core.SecretTypeBasicAuth:
		_, usernameFieldExists := secret.Data[core.BasicAuthUsernameKey]
		_, passwordFieldExists := secret.Data[core.BasicAuthPasswordKey]

		// username or password might be empty, but the field must be present
		if !usernameFieldExists && !passwordFieldExists {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.BasicAuthUsernameKey), ""))
			allErrs = append(allErrs, field.Required(dataPath.Key(core.BasicAuthPasswordKey), ""))
			break
		}
	case core.SecretTypeSSHAuth:
		if len(secret.Data[core.SSHAuthPrivateKey]) == 0 {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.SSHAuthPrivateKey), ""))
			break
		}

	case core.SecretTypeTLS:
		if _, exists := secret.Data[core.TLSCertKey]; !exists {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.TLSCertKey), ""))
		}
		if _, exists := secret.Data[core.TLSPrivateKeyKey]; !exists {
			allErrs = append(allErrs, field.Required(dataPath.Key(core.TLSPrivateKeyKey), ""))
		}
	// TODO: Verify that the key matches the cert.
	default:
		// no-op
	}

	return allErrs
}

// ValidateSecretUpdate tests if required fields in the Secret are set.
func ValidateSecretUpdate(newSecret, oldSecret *core.InternalSecret) field.ErrorList {
	allErrs := apivalidation.ValidateObjectMetaUpdate(&newSecret.ObjectMeta, &oldSecret.ObjectMeta, field.NewPath("metadata"))

	allErrs = append(allErrs, apivalidation.ValidateImmutableField(newSecret.Type, oldSecret.Type, field.NewPath("type"))...)
	if oldSecret.Immutable != nil && *oldSecret.Immutable {
		if newSecret.Immutable == nil || !*newSecret.Immutable {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("immutable"), "field is immutable when `immutable` is set"))
		}
		if !reflect.DeepEqual(newSecret.Data, oldSecret.Data) {
			allErrs = append(allErrs, field.Forbidden(field.NewPath("data"), "field is immutable when `immutable` is set"))
		}
		// We don't validate StringData, as it was already converted back to Data
		// before validation is happening.
	}

	allErrs = append(allErrs, ValidateSecret(newSecret)...)
	return allErrs
}
