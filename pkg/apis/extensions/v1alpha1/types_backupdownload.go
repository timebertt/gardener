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

var _ Object = (*BackupDownload)(nil)

// BackupDownloadResource is a constant for the name of the BackupDownload resource.
const BackupDownloadResource = "BackupDownload"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:scope=Namespaced,path=backupdownloads,shortName=bd,singular=backupdownload
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Type,JSONPath=".spec.type",type=string,description="The type of the cloud provider for this resource."
// +kubebuilder:printcolumn:name=Entry,JSONPath=".spec.entryName",type=string,description="The backup entry that the data should be downloaded from."
// +kubebuilder:printcolumn:name=State,JSONPath=".status.lastOperation.state",type=string,description="status of the last operation, one of Aborted, Processing, Succeeded, Error, Failed"
// +kubebuilder:printcolumn:name=Age,JSONPath=".metadata.creationTimestamp",type=date,description="creation timestamp"

// BackupDownload is a specification for backup download.
type BackupDownload struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Specification of the BackupDownload.
	// If the object's deletion timestamp is set, this field is immutable.
	Spec BackupDownloadSpec `json:"spec"`
	// +optional
	Status BackupDownloadStatus `json:"status"`
}

// GetExtensionSpec implements Object.
func (i *BackupDownload) GetExtensionSpec() Spec {
	return &i.Spec
}

// GetExtensionStatus implements Object.
func (i *BackupDownload) GetExtensionStatus() Status {
	return &i.Status
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BackupDownloadList is a list of BackupDownload resources.
type BackupDownloadList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// Items is the list of BackupDownload.
	Items []BackupDownload `json:"items"`
}

// BackupDownloadSpec is the spec for an BackupDownload resource.
type BackupDownloadSpec struct {
	// DefaultSpec is a structure containing common fields used by all extension resources.
	DefaultSpec `json:",inline"`
	// EntryName is a reference to a BackupEntry where the data should be downloaded from.
	EntryName string `json:"entryName"`
	// FilePath is the path in the BackupEntry where the data should be downloaded from.
	FilePath string `json:"filePath"`
}

// BackupDownloadStatus is the status for an BackupDownload resource.
type BackupDownloadStatus struct {
	// DefaultStatus is a structure containing common fields used by all extension resources.
	DefaultStatus `json:",inline"`
	// Data is the binary data that was downloaded.
	// +optional
	Data []byte `json:"data,omitempty"`
}
