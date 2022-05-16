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
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/gardener/gardener/extensions/pkg/controller/controlplane/genericactuator"
	"github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/gardener/gardener/pkg/utils/kubernetes"
	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
)

const certificateReconcilerName = "webhook-certificate"

// Reconciler is a simple reconciler that manages the webhook CA and server certificate using a secrets manager.
// It runs Generate for both secret configs followed by Cleanup every SyncPeriod and updates the WebhookConfigurations
// accordingly with the new CA bundle.
type Reconciler struct {
	// SyncPeriod is the frequency with which to reload the server cert. Defaults to 5m.
	SyncPeriod *time.Duration
	// ServerPort is the port that the webhook server is exposed on. Defaults to manager.GetWebhookServer().Port.
	ServerPort int
	// SeedWebhookConfig is the webhook configuration to reconcile in the Seed cluster.
	SeedWebhookConfig client.Object
	// ShootWebhookConfig is the webhook configuration to reconcile in all Shoot clusters.
	// This object is supposed to be shared with the ControlPlane actuator. I.e. if the CA bundle changes, this Reconciler
	// updates the CA bundle in this object, so that the ControlPlane actuator will configure the correct (the new) CA
	// bundle for newly created shoots without waiting for another reconciliation of this Reconciler.
	ShootWebhookConfig client.Object
	// CASecretName is the CA config name.
	CASecretName string
	// ServerSecretName is the server certificate config name.
	ServerSecretName string
	// Namespace where the server certificate secret should be located in.
	Namespace string
	// Identity of the secrets manager used for managing the secret.
	Identity string
	// Name and provider type of the extension.
	ProviderName, ProviderType string
	// Mode is the webhook client config mode.
	Mode string
	// URL is the URL that is used to register the webhooks in Kubernetes.
	URL string

	client client.Client
}

// AddToManager generates webhook CA and server cert if it doesn't exist on the cluster yet. Then, it adds Reconciler
// to the given manager in order to periodically regenerate the webhook secrets.
func (r *Reconciler) AddToManager(ctx context.Context, mgr manager.Manager) error {
	if r.SyncPeriod == nil {
		defaultSyncPeriod := 5 * time.Minute
		r.SyncPeriod = &defaultSyncPeriod
	}

	if r.ServerPort == 0 {
		r.ServerPort = mgr.GetWebhookServer().Port
	}

	if r.client == nil {
		r.client = mgr.GetClient()
	}

	present, err := isWebhookServerSecretPresent(ctx, mgr.GetAPIReader(), r.ServerSecretName, r.Namespace, r.Identity)
	if err != nil {
		return err
	}

	// if webhook CA and server cert have not been generated yet, we need to generate them for the first time now,
	// otherwise the webhook server will not be able to start (which is a non-leader election runnable and is therefore
	// started before this controller)
	if !present {
		// cache is not started yet, we need an uncached client for the initial setup
		uncachedClient, err := client.NewDelegatingClient(client.NewDelegatingClientInput{
			Client:      r.client,
			CacheReader: mgr.GetAPIReader(),
		})

		sm, err := r.newSecretsManager(ctx, mgr.GetLogger(), uncachedClient)
		if err != nil {
			return fmt.Errorf("failed to create new SecretsManager: %w", err)
		}

		if _, err = r.generateWebhookCA(ctx, sm); err != nil {
			return err
		}

		if r.ShootWebhookConfig != nil {
			// update shoot webhook config that is used by the ControlPlane actuator with the freshly created CA bundle
			caBundleSecret, found := sm.Get(r.CASecretName)
			if !found {
				return fmt.Errorf("secret %q not found", r.CASecretName)
			}
			if err := webhook.InjectCABundleIntoWebhookConfig(r.ShootWebhookConfig, caBundleSecret.Data[secretutils.DataKeyCertificateBundle]); err != nil {
				return err
			}
		}

		if _, err = r.generateWebhookServerCert(ctx, sm); err != nil {
			return err
		}
	}

	// remove legacy webhook cert secret
	// TODO(timebertt): remove this in a future release
	if err := kubernetes.DeleteObject(ctx, r.client,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: "gardener-extension-webhook-cert"}},
	); err != nil {
		return err
	}

	// add controller, that regenerates the CA and server cert secrets periodically
	ctrl, err := controller.New(certificateReconcilerName, mgr, controller.Options{
		Reconciler:   r,
		RecoverPanic: true,
		// if going into exponential backoff, wait at most the configured sync period
		RateLimiter: workqueue.NewWithMaxWaitRateLimiter(workqueue.DefaultControllerRateLimiter(), *r.SyncPeriod),
	})
	if err != nil {
		return err
	}

	return ctrl.Watch(triggerOnce, nil)
}

func (r *Reconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	sm, err := r.newSecretsManager(ctx, log, r.client)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to create new SecretsManager: %w", err)
	}

	caSecret, err := r.generateWebhookCA(ctx, sm)
	if err != nil {
		return reconcile.Result{}, err
	}
	caBundleSecret, found := sm.Get(r.CASecretName)
	if !found {
		return reconcile.Result{}, fmt.Errorf("secret %q not found", r.CASecretName)
	}

	log = log.WithValues("secretNamespace", r.Namespace, "identity", r.Identity, "caSecretName", caSecret.Name, "caBundleSecretName", caBundleSecret.Name)
	log.Info("Generated webhook CA")

	if r.ShootWebhookConfig != nil {
		// update shoot webhook config that is used by the ControlPlane actuator with the freshly created CA bundle
		if err := webhook.InjectCABundleIntoWebhookConfig(r.ShootWebhookConfig, caBundleSecret.Data[secretutils.DataKeyCertificateBundle]); err != nil {
			return reconcile.Result{}, err
		}
	}

	serverSecret, err := r.generateWebhookServerCert(ctx, sm)
	if err != nil {
		return reconcile.Result{}, err
	}
	log.Info("Generated webhook server cert", "serverSecretName", serverSecret.Name)

	log.Info("Updating seed webhook config with new CA bundle", "webhookConfig", r.SeedWebhookConfig)
	if err := r.reconcileSeedWebhookConfig(ctx, caBundleSecret); err != nil {
		return reconcile.Result{}, fmt.Errorf("error reconciling seed webhook config: %w", err)
	}

	if r.ShootWebhookConfig != nil {
		log.Info("Updating all shoot webhook configs with new CA bundle", "webhookConfig", r.ShootWebhookConfig)

		// reconcile all shoot webhook configs with the freshly created CA bundle
		if err := genericactuator.ReconcileShootWebhooksForAllNamespaces(ctx, r.client, r.ProviderName, r.ProviderType, r.ServerPort, r.ShootWebhookConfig); err != nil {
			return reconcile.Result{}, fmt.Errorf("error reconciling all shoot webhook configs: %w", err)
		}
	}

	if err := sm.Cleanup(ctx); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: *r.SyncPeriod}, nil
}

func (r *Reconciler) reconcileSeedWebhookConfig(ctx context.Context, caBundleSecret *corev1.Secret) error {
	// copy object so that we don't lose its name on API/client errors
	config := r.SeedWebhookConfig.DeepCopyObject().(client.Object)

	if err := r.client.Get(ctx, client.ObjectKeyFromObject(config), config); err != nil {
		return err
	}

	patch := client.MergeFromWithOptions(config.DeepCopyObject().(client.Object), client.MergeFromWithOptimisticLock{})
	if err := webhook.InjectCABundleIntoWebhookConfig(config, caBundleSecret.Data[secretutils.DataKeyCertificateBundle]); err != nil {
		return err
	}

	return r.client.Patch(ctx, config, patch)
}

func isWebhookServerSecretPresent(ctx context.Context, c client.Reader, secretName, namespace, identity string) (bool, error) {
	secretList := &corev1.SecretList{}
	if err := c.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
		secretsmanager.LabelKeyName:            secretName,
		secretsmanager.LabelKeyManagedBy:       secretsmanager.LabelValueSecretsManager,
		secretsmanager.LabelKeyManagerIdentity: identity,
	}); err != nil {
		return false, err
	}

	return len(secretList.Items) > 0, nil
}

func (r *Reconciler) newSecretsManager(ctx context.Context, log logr.Logger, c client.Client) (secretsmanager.Interface, error) {
	return secretsmanager.New(
		ctx,
		log.WithName("secretsmanager"),
		&clock.RealClock{},
		c,
		r.Namespace,
		r.Identity,
		secretsmanager.Config{CASecretAutoRotation: true},
	)
}

func (r *Reconciler) generateWebhookCA(ctx context.Context, sm secretsmanager.Interface) (*corev1.Secret, error) {
	return sm.Generate(ctx, getWebhookCAConfig(r.CASecretName),
		secretsmanager.Rotate(secretsmanager.KeepOld), secretsmanager.IgnoreOldSecretsAfter(ignoreOldSecretsAfter))
}

func (r *Reconciler) generateWebhookServerCert(ctx context.Context, sm secretsmanager.Interface) (*corev1.Secret, error) {
	// use current CA for signing server cert to prevent mismatches when dropping the old CA from the webhook config
	return sm.Generate(ctx, getWebhookServerCertConfig(r.ServerSecretName, r.Namespace, r.Mode, r.URL),
		secretsmanager.SignedByCA(r.CASecretName, secretsmanager.UseCurrentCA))
}

var (
	validity              = 30 * 24 * time.Hour // 30d
	ignoreOldSecretsAfter = 24 * time.Hour
)

func getWebhookCAConfig(name string) *secretutils.CertificateSecretConfig {
	return &secretutils.CertificateSecretConfig{
		Name:       name,
		CommonName: name,
		CertType:   secretutils.CACert,
		Validity:   &validity,
	}
}

func getWebhookServerCertConfig(name, namespace, mode, url string) *secretutils.CertificateSecretConfig {
	var (
		dnsNames    []string
		ipAddresses []net.IP

		serverName     = url
		serverNameData = strings.SplitN(url, ":", 3)
	)

	if len(serverNameData) == 2 {
		serverName = serverNameData[0]
	}

	switch mode {
	case webhook.ModeURL:
		if addr := net.ParseIP(url); addr != nil {
			ipAddresses = []net.IP{addr}
		} else {
			dnsNames = []string{serverName}
		}

	case webhook.ModeService:
		dnsNames = []string{
			fmt.Sprintf("gardener-extension-%s", name),
		}
		if namespace != "" {
			dnsNames = append(dnsNames,
				fmt.Sprintf("gardener-extension-%s.%s", name, namespace),
				fmt.Sprintf("gardener-extension-%s.%s.svc", name, namespace),
			)
		}
	}

	return &secretutils.CertificateSecretConfig{
		Name:        name,
		CommonName:  name,
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
		CertType:    secretutils.ServerCert,

		SkipPublishingCACertificate: true,
	}
}

// triggerOnce is a source.Source that simply triggers the reconciler once with an empty reconcile.Request.
var triggerOnce = source.Func(func(_ context.Context, _ handler.EventHandler, q workqueue.RateLimitingInterface, _ ...predicate.Predicate) error {
	q.Add(reconcile.Request{})
	return nil
})
