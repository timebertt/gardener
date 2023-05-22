// Copyright 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package downloader

import (
	_ "embed"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/user"
	bootstraptokenapi "k8s.io/cluster-bootstrap/token/api"
	"k8s.io/utils/pointer"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component/logging/kuberbacproxy"
	"github.com/gardener/gardener/pkg/utils"
	"github.com/gardener/gardener/pkg/utils/images"
	"github.com/gardener/gardener/pkg/utils/imagevector"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/gardener/gardener/pkg/utils/secrets"
)

//go:embed scripts/gardener-node-init.sh
var nodeInitScript string

const (
	// Name is a constant for the cloud-config-downloader.
	Name = "gardener-node-init"
	// UnitName is the name of the cloud-config-downloader service.
	UnitName = Name + ".service"
	// SecretName is a constant for the secret name for the cloud-config-downloader's shoot access secret.
	SecretName = Name
	// UnitRestartSeconds is the number of seconds after which the cloud-config-downloader unit will be restarted.
	UnitRestartSeconds = 30

	// DataKeyScript is the key whose value is the to-be-executed cloud-config user-data script inside a data map of a
	// Kubernetes secret object.
	DataKeyScript = "script"
	// AnnotationKeyChecksum is the key of an annotation on a Secret object whose value is the checksum of the cloud
	// config user data stored in the data map of this Secret.
	AnnotationKeyChecksum = "checksum/data-script"

	// PathCCDDirectory is a constant for the path of the cloud-config-downloader unit.
	PathCCDDirectory = "/var/lib/" + Name
	// PathCredentialsDirectory is a constant for the path of the cloud-config-downloader credentials used to download
	// the cloud-config user-data.
	PathCredentialsDirectory = PathCCDDirectory + "/credentials"
	// PathDownloadsDirectory is a constant for the path of the cloud-config-downloader credentials used for storing the
	// downloaded content.
	PathDownloadsDirectory = PathCCDDirectory + "/downloads"

	// PathCCDScript is a constant for the path of the script containing the instructions to download the cloud-config
	// user-data.
	PathCCDScript = PathCCDDirectory + "/gardener-node-init.sh"
	// PathCCDScriptChecksum is a constant for the path of the file containing md5 has of PathCCDScript.
	PathCCDScriptChecksum = PathCCDDirectory + "/download-cloud-config.md5"
	// PathCredentialsServer is a constant for a path containing the 'server' part for the download.
	PathCredentialsServer = PathCredentialsDirectory + "/server"
	// PathCredentialsCACert is a constant for a path containing the 'CA certificate' credentials part for the download.
	PathCredentialsCACert = PathCredentialsDirectory + "/ca.crt"
	// PathCredentialsClientCert is a constant for a path containing the 'client certificate' credentials part for the
	// download.
	PathCredentialsClientCert = PathCredentialsDirectory + "/client.crt"
	// PathCredentialsClientKey is a constant for a path containing the 'client private key' credentials part for the
	// download.
	PathCredentialsClientKey = PathCredentialsDirectory + "/client.key"
	// PathCredentialsToken is a constant for a path containing the shoot access 'token' for the cloud-config-downloader.
	PathCredentialsToken = PathCredentialsDirectory + "/token"
	// PathBootstrapToken is the path of a file on the shoot worker nodes in which the bootstrap token for the kubelet
	// bootstrap is stored.
	PathBootstrapToken = PathCredentialsDirectory + "/bootstrap-token"
	// BootstrapTokenPlaceholder is the token that is expected to be replaced by the worker controller with the actual
	// token.
	BootstrapTokenPlaceholder = "<<BOOTSTRAP_TOKEN>>"
	// PathDownloadedCloudConfig is the path on the shoot worker nodes at which the downloaded cloud-config user-data
	// will be stored.
	PathDownloadedCloudConfig = PathDownloadsDirectory + "/cloud_config"
	// PathDownloadedExecutorScript is the path on the shoot worker nodes at which the downloaded executor script will
	// be stored.
	PathDownloadedExecutorScript = PathDownloadsDirectory + "/execute-cloud-config.sh"
	// PathDownloadedCloudConfigChecksum is the path on the shoot worker nodes at which the checksum of the downloaded
	// cloud-config user-data will be stored.
	PathDownloadedCloudConfigChecksum = PathDownloadsDirectory + "/execute-cloud-config-checksum"
)

func ImageRefToLayerURL(image string, worker gardencorev1beta1.Worker) (*url.URL, string, error) {
	// TODO(rfranzke): figure this out after breakfast
	image = strings.ReplaceAll(image, "localhost:5001", "garden.local.gardener.cloud:5001")
	imageRef, err := name.ParseReference(image, name.Insecure)
	if err != nil {
		return nil, "", err
	}

	arch := v1beta1constants.ArchitectureAMD64
	if workerArch := worker.Machine.Architecture; workerArch != nil {
		arch = *workerArch
	}

	remoteImage, err := remote.Image(imageRef, remote.WithPlatform(v1.Platform{OS: "linux", Architecture: arch}))
	if err != nil {
		return nil, "", err
	}

	imageConfig, err := remoteImage.ConfigFile()
	if err != nil {
		return nil, "", err
	}
	entrypoint := imageConfig.Config.Entrypoint[0]

	manifest, err := remoteImage.Manifest()
	if err != nil {
		return nil, "", err
	}

	finalLayer := manifest.Layers[len(manifest.Layers)-1]

	// This is what the library does internally as well. It doesn't expose a func for it though.
	return &url.URL{
		Scheme: imageRef.Context().Scheme(),
		Host:   imageRef.Context().RegistryStr(),
		Path:   fmt.Sprintf("/v2/%s/%s/%s", imageRef.Context().RepositoryStr(), "blobs", finalLayer.Digest),
	}, entrypoint, nil
}

// Config returns the units and the files for the OperatingSystemConfig that downloads the actual cloud-config user
// data.
// ### !CAUTION! ###
// Most cloud providers have a limit of 16 KB regarding the user-data that may be sent during VM creation.
// The result of this operating system config is exactly the user-data that will be sent to the providers.
// We must not exceed the 16 KB, so be careful when extending/changing anything in here.
// ### !CAUTION! ###
func Config(cloudConfigUserDataSecretName, apiServerURL, clusterCASecretName string, imageVector imagevector.ImageVector, worker gardencorev1beta1.Worker) ([]extensionsv1alpha1.Unit, []extensionsv1alpha1.File, error) {
	image, err := imageVector.FindImage(images.ImageNameGardenlet)
	if err != nil {
		return nil, nil, err
	}

	layerURL, binaryPath, err := ImageRefToLayerURL(image.String(), worker)
	if err != nil {
		return nil, nil, err
	}

	units := []extensionsv1alpha1.Unit{
		{
			Name:    UnitName,
			Command: pointer.String("start"),
			Enable:  pointer.Bool(true),
			Content: pointer.String(`[Unit]
Description=Downloads the gardener-node-agent binary from the registry and bootstraps it.
[Service]
Restart=always
RestartSec=` + strconv.Itoa(UnitRestartSeconds) + `
RuntimeMaxSec=120
EnvironmentFile=/etc/environment
ExecStart=` + fmt.Sprintf("%s %s %s", PathCCDScript, layerURL.String(), binaryPath) + `
[Install]
WantedBy=multi-user.target`),
		},
	}

	files := []extensionsv1alpha1.File{
		{
			Path:        PathCredentialsServer,
			Permissions: pointer.Int32(0644),
			Content: extensionsv1alpha1.FileContent{
				Inline: &extensionsv1alpha1.FileContentInline{
					Encoding: "b64",
					Data:     utils.EncodeBase64([]byte(apiServerURL)),
				},
			},
		},
		{
			Path:        PathCredentialsCACert,
			Permissions: pointer.Int32(0644),
			Content: extensionsv1alpha1.FileContent{
				SecretRef: &extensionsv1alpha1.FileContentSecretRef{
					Name:    clusterCASecretName,
					DataKey: secrets.DataKeyCertificateBundle,
				},
			},
		},
		{
			Path:        PathCCDScript,
			Permissions: pointer.Int32(0744),
			Content: extensionsv1alpha1.FileContent{
				Inline: &extensionsv1alpha1.FileContentInline{
					Encoding: "b64",
					Data:     utils.EncodeBase64([]byte(nodeInitScript)),
				},
			},
		},
		{
			Path:        PathBootstrapToken,
			Permissions: pointer.Int32(0644),
			Content: extensionsv1alpha1.FileContent{
				Inline: &extensionsv1alpha1.FileContentInline{
					Data: BootstrapTokenPlaceholder,
				},
				TransmitUnencoded: pointer.Bool(true),
			},
		},
	}

	return units, files, nil
}

// GenerateRBACResourcesData returns a map of serialized Kubernetes resources that allow the cloud-config-downloader to
// access the list of given secrets. Additionally, serialized resources providing permissions to allow initiating the
// Kubernetes TLS bootstrapping process will be returned.
func GenerateRBACResourcesData(secretNames []string) (map[string][]byte, error) {
	var (
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      Name,
				Namespace: metav1.NamespaceSystem,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: append(secretNames, Name, kuberbacproxy.ValitailTokenSecretName),
					Verbs:         []string{"get"},
				},
			},
		}

		roleBinding = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      Name,
				Namespace: metav1.NamespaceSystem,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     "Role",
				Name:     role.Name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind: rbacv1.GroupKind,
					Name: bootstraptokenapi.BootstrapDefaultGroup,
				},
				{
					Kind:      rbacv1.ServiceAccountKind,
					Name:      Name,
					Namespace: metav1.NamespaceSystem,
				},
			},
		}

		clusterRoleBindingNodeBootstrapper = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "system:node-bootstrapper",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     "ClusterRole",
				Name:     "system:node-bootstrapper",
			},
			Subjects: []rbacv1.Subject{{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     rbacv1.GroupKind,
				Name:     bootstraptokenapi.BootstrapDefaultGroup,
			}},
		}

		clusterRoleBindingNodeClient = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "system:certificates.k8s.io:certificatesigningrequests:nodeclient",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     "ClusterRole",
				Name:     "system:certificates.k8s.io:certificatesigningrequests:nodeclient",
			},
			Subjects: []rbacv1.Subject{{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     rbacv1.GroupKind,
				Name:     bootstraptokenapi.BootstrapDefaultGroup,
			}},
		}

		clusterRoleBindingSelfNodeClient = &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: "system:certificates.k8s.io:certificatesigningrequests:selfnodeclient",
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     "ClusterRole",
				Name:     "system:certificates.k8s.io:certificatesigningrequests:selfnodeclient",
			},
			Subjects: []rbacv1.Subject{{
				APIGroup: rbacv1.SchemeGroupVersion.Group,
				Kind:     rbacv1.GroupKind,
				Name:     user.NodesGroup,
			}},
		}
	)

	return managedresources.
		NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer).
		AddAllAndSerialize(
			role,
			roleBinding,
			clusterRoleBindingNodeBootstrapper,
			clusterRoleBindingNodeClient,
			clusterRoleBindingSelfNodeClient,
		)
}
