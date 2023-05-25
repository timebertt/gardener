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
	"strings"

	"github.com/go-test/deep"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
)

// ValidateBackupDownload validates a BackupDownload object.
func ValidateBackupDownload(be *extensionsv1alpha1.BackupDownload) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, apivalidation.ValidateObjectMeta(&be.ObjectMeta, true, apivalidation.NameIsDNSSubdomain, field.NewPath("metadata"))...)
	allErrs = append(allErrs, ValidateBackupDownloadSpec(&be.Spec, field.NewPath("spec"))...)

	return allErrs
}

// ValidateBackupDownloadUpdate validates a BackupDownload object before an update.
func ValidateBackupDownloadUpdate(new, old *extensionsv1alpha1.BackupDownload) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, apivalidation.ValidateObjectMetaUpdate(&new.ObjectMeta, &old.ObjectMeta, field.NewPath("metadata"))...)
	allErrs = append(allErrs, ValidateBackupDownloadSpecUpdate(&new.Spec, &old.Spec, new.DeletionTimestamp != nil, field.NewPath("spec"))...)
	allErrs = append(allErrs, ValidateBackupDownload(new)...)

	return allErrs
}

// ValidateBackupDownloadSpec validates the specification of a BackupDownload object.
func ValidateBackupDownloadSpec(spec *extensionsv1alpha1.BackupDownloadSpec, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(spec.Type) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("type"), "field is required"))
	}

	if len(spec.EntryName) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("entryName"), "field is required"))
	}

	if len(spec.FilePath) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("filePath"), "field is required"))
	}

	return allErrs
}

// ValidateBackupDownloadSpecUpdate validates the spec of a BackupDownload object before an update.
func ValidateBackupDownloadSpecUpdate(new, old *extensionsv1alpha1.BackupDownloadSpec, deletionTimestampSet bool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if deletionTimestampSet && !apiequality.Semantic.DeepEqual(new, old) {
		if diff := deep.Equal(new, old); diff != nil {
			return field.ErrorList{field.Forbidden(fldPath, strings.Join(diff, ","))}
		}
		return apivalidation.ValidateImmutableField(new, old, fldPath)
	}

	allErrs = append(allErrs, apivalidation.ValidateImmutableField(new.Type, old.Type, fldPath.Child("type"))...)
	allErrs = append(allErrs, apivalidation.ValidateImmutableField(new.EntryName, old.EntryName, fldPath.Child("entryName"))...)
	allErrs = append(allErrs, apivalidation.ValidateImmutableField(new.FilePath, old.FilePath, fldPath.Child("filePath"))...)

	return allErrs
}

// ValidateBackupDownloadStatus validates the status of a BackupDownload object.
func ValidateBackupDownloadStatus(status *extensionsv1alpha1.BackupDownloadStatus, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	return allErrs
}

// ValidateBackupDownloadStatusUpdate validates the status field of a BackupDownload object before an update.
func ValidateBackupDownloadStatusUpdate(newStatus, oldStatus *extensionsv1alpha1.BackupDownloadStatus, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, apivalidation.ValidateImmutableField(newStatus.Data, oldStatus.Data, fldPath.Child("data"))...)

	return allErrs
}
