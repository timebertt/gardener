// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package certificates

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// StartManagingCertificates adds reconcilers to the given manager that manage the webhook certificates, namely
// - generate and auto-rotate the webhook CA and server cert using a secrets manager (in leader only)
// - fetch current webhook server cert and write it to disk for the webhook server to pick up (in all replicas)
func StartManagingCertificates(ctx context.Context, mgr manager.Manager, seedWebhookConfig, shootWebhookConfig client.Object, extensionName, providerType, namespace, mode, url string) error {
	var (
		identity         = "gardener-extension-" + extensionName + "-webhook"
		caSecretName     = "ca-" + extensionName + "-webhook"
		serverSecretName = extensionName + "-webhook" + "-server"
	)

	// first, add reconciler that manages the certificates and injects them into webhook configs
	// (only running in the leader or once if no secrets have been generated yet)
	if err := (&Reconciler{
		SeedWebhookConfig:  seedWebhookConfig,
		ShootWebhookConfig: shootWebhookConfig,
		CASecretName:       caSecretName,
		ServerSecretName:   serverSecretName,
		Namespace:          namespace,
		Identity:           identity,
		ProviderName:       extensionName,
		ProviderType:       providerType,
		Mode:               mode,
		URL:                url,
	}).AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to add webhook server certificate reconciler: %w", err)
	}

	// secondly, add reloader that fetches the managed certificates and writes it to the webhook server's cert dir
	// (running in all replicas)
	if err := (&Reloader{
		SecretName: serverSecretName,
		Namespace:  namespace,
		Identity:   identity,
	}).AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed to add webhook server certificate reloader: %w", err)
	}

	return nil
}

// GenerateUnmanagedCertificates generates a one-off CA and server cert for a webhook server. The server certificate and
// key are written to certDir. This is useful for local development.
func GenerateUnmanagedCertificates(certDir, mode, name, url string) ([]byte, error) {
	caConfig := getWebhookCAConfig(name)
	// we want to use the default validity of 10 years here, because we don't auto-renew certificates
	caConfig.Validity = nil
	caCert, err := caConfig.GenerateCertificate()
	if err != nil {
		return nil, err
	}

	serverConfig := getWebhookServerCertConfig(name, "", mode, url)
	serverConfig.SigningCA = caCert
	serverCert, err := serverConfig.GenerateCertificate()
	if err != nil {
		return nil, err
	}

	return caCert.CertificatePEM, writeCertificates(certDir, serverCert.CertificatePEM, serverCert.PrivateKeyPEM)
}
