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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ Object = (*BackupUpload)(nil)

// BackupUploadResource is a constant for the name of the BackupUpload resource.
const BackupUploadResource = "BackupUpload"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Namespaced,path=backupuploads,shortName=bu,singular=backupupload
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Type,JSONPath=".spec.type",type=string,description="The type of the cloud provider for this resource."
// +kubebuilder:printcolumn:name=Entry,JSONPath=".spec.entryName",type=string,description="The backup entry that the data should be uploaded to."
// +kubebuilder:printcolumn:name=State,JSONPath=".status.lastOperation.state",type=string,description="status of the last operation, one of Aborted, Processing, Succeeded, Error, Failed"
// +kubebuilder:printcolumn:name=Age,JSONPath=".metadata.creationTimestamp",type=date,description="creation timestamp"

// BackupUpload is a specification for backup upload.
type BackupUpload struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Specification of the BackupUpload.
	// If the object's deletion timestamp is set, this field is immutable.
	Spec BackupUploadSpec `json:"spec"`
	// +optional
	Status BackupUploadStatus `json:"status"`
}

// GetExtensionSpec implements Object.
func (i *BackupUpload) GetExtensionSpec() Spec {
	return &i.Spec
}

// GetExtensionStatus implements Object.
func (i *BackupUpload) GetExtensionStatus() Status {
	return &i.Status
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BackupUploadList is a list of BackupUpload resources.
type BackupUploadList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of BackupUpload.
	Items []BackupUpload `json:"items"`
}

// BackupUploadSpec is the spec for an BackupUpload resource.
type BackupUploadSpec struct {
	// DefaultSpec is a structure containing common fields used by all extension resources.
	DefaultSpec `json:",inline"`
	// EntryName is a reference to a BackupEntry where the data should be uploaded to.
	EntryName string `json:"entryName"`
	// FilePath is the path in the BackupEntry where the data should be uploaded to.
	FilePath string `json:"filePath"`
	// Data is the binary data that should be uploaded.
	Data []byte `json:"data"`
}

// BackupUploadStatus is the status for an BackupUpload resource.
type BackupUploadStatus struct {
	// DefaultStatus is a structure containing common fields used by all extension resources.
	DefaultStatus `json:",inline"`
}
