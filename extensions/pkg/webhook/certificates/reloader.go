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
	"os"
	"path/filepath"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
)

const certificateReloaderName = "webhook-certificate-reloader"

// Reloader is a simple reconciler that retrieves the current webhook server certificate managed by a secrets manager
// every SyncPeriod and writes it to CertDir.
type Reloader struct {
	// SyncPeriod is the frequency with which to reload the server cert. Defaults to 5m.
	SyncPeriod *time.Duration
	// SecretName is the server certificate config name.
	SecretName string
	// Namespace where the server certificate secret is located in.
	Namespace string
	// Identity of the secrets manager used for managing the secret.
	Identity string
	// CertDir is the directory to write the certificates to. Defaults to the manager's webhook cert dir.
	CertDir string

	reader client.Reader

	lock              sync.Mutex
	currentSecretName string
}

// AddToManager does an initial retrieval of an existing webhook server secret and then adds Reloader to the given
// manager in order to periodically reload the secret from the cluster.
func (r *Reloader) AddToManager(ctx context.Context, mgr manager.Manager) error {
	if r.SyncPeriod == nil {
		defaultSyncPeriod := 5 * time.Minute
		r.SyncPeriod = &defaultSyncPeriod
	}

	if r.CertDir == "" {
		r.CertDir = mgr.GetWebhookServer().CertDir
	}

	if r.reader == nil {
		r.reader = mgr.GetClient()
	}

	// initial retrieval of server cert, needed in order for the webhook server to start successfully
	found, _, serverCert, serverKey, err := r.getServerCert(ctx, mgr.GetAPIReader())
	if err != nil {
		return err
	}

	if !found {
		// if we can't find a server cert secret on startup, the leader has not yet generated one
		// exit and retry on next restart
		return fmt.Errorf("couldn't find webhook server secret with name %q managed by secrets manager %q in namespace %q", r.SecretName, r.Identity, r.Namespace)
	}

	if err = writeCertificates(r.CertDir, serverCert, serverKey); err != nil {
		return err
	}

	// add controller, that reloads the server cert secret periodically
	ctrl, err := controller.NewUnmanaged(certificateReloaderName, mgr, controller.Options{
		Reconciler:   r,
		RecoverPanic: true,
		// if going into exponential backoff, wait at most the configured sync period
		RateLimiter: workqueue.NewWithMaxWaitRateLimiter(workqueue.DefaultControllerRateLimiter(), *r.SyncPeriod),
	})
	if err != nil {
		return err
	}

	if err = ctrl.Watch(triggerOnce, nil); err != nil {
		return err
	}

	// we need to run this controller in all replicas even if they aren't leader right now, so that webhook servers
	// in stand-by replicas reload rotated server certificates as well
	return mgr.Add(nonLeaderElectionRunnable{ctrl})
}

func (r *Reloader) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx).WithValues(
		"secretConfigName", r.SecretName, "secretNamespace", r.Namespace, "identity", r.Identity, "certDir", r.CertDir,
	)

	log.V(1).Info("Reloading server certificate from secret")

	found, secretName, serverCert, serverKey, err := r.getServerCert(ctx, r.reader)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error retrieving secret %q from namespace %q", r.SecretName, r.Namespace)
	}

	if !found {
		log.Info("Couldn't find webhook server secret, retrying")
		return reconcile.Result{Requeue: true}, nil
	}

	log = log.WithValues("secretName", secretName)

	r.lock.Lock()
	defer r.lock.Unlock()

	// prevent unnecessary disk writes
	if secretName == r.currentSecretName {
		log.V(1).Info("Secret already written to disk, checking again later")
		return reconcile.Result{RequeueAfter: *r.SyncPeriod}, nil
	}

	log.Info("Found new secret, writing certificate to disk")
	if err = writeCertificates(r.CertDir, serverCert, serverKey); err != nil {
		return reconcile.Result{}, err
	}

	r.currentSecretName = secretName
	return reconcile.Result{RequeueAfter: *r.SyncPeriod}, nil
}

func (r *Reloader) getServerCert(ctx context.Context, reader client.Reader) (bool, string, []byte, []byte, error) {
	secretList := &corev1.SecretList{}
	if err := reader.List(ctx, secretList, client.InNamespace(r.Namespace), client.MatchingLabels{
		secretsmanager.LabelKeyName:            r.SecretName,
		secretsmanager.LabelKeyManagedBy:       secretsmanager.LabelValueSecretsManager,
		secretsmanager.LabelKeyManagerIdentity: r.Identity,
	}); err != nil {
		return false, "", nil, nil, err
	}

	if len(secretList.Items) != 1 {
		return false, "", nil, nil, nil
	}

	s := secretList.Items[0]
	return true, s.Name, s.Data[secretutils.DataKeyCertificate], s.Data[secretutils.DataKeyPrivateKey], nil
}

func writeCertificates(certDir string, serverCert, serverKey []byte) error {
	var (
		serverKeyPath  = filepath.Join(certDir, secretutils.DataKeyPrivateKey)
		serverCertPath = filepath.Join(certDir, secretutils.DataKeyCertificate)
	)

	if err := os.MkdirAll(certDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(serverKeyPath, serverKey, 0666); err != nil {
		return err
	}
	return os.WriteFile(serverCertPath, serverCert, 0666)
}

// nonLeaderElectionRunnable wraps another manager.Runnable to make it run without leader election
type nonLeaderElectionRunnable struct {
	manager.Runnable
}

// NeedLeaderElection implements the LeaderElectionRunnable interface to indicate that this runnable doesn't
// need leader election.
func (n nonLeaderElectionRunnable) NeedLeaderElection() bool {
	return false
}
