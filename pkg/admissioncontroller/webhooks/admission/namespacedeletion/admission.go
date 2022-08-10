// Copyright (c) 2018 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package namespacedeletion

import (
	"context"
	"fmt"
	"net/http"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	acadmission "github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
)

// WebhookPath is the HTTP handler path for this admission webhook handler.
const WebhookPath = "/webhooks/validate-namespace-deletion"

var namespaceGVK = metav1.GroupVersionKind{Group: "", Kind: "Namespace", Version: "v1"}

// Handler is a webhook handler for validating namespace deletions.
type Handler struct {
	Cache     cache.Cache
	APIReader client.Reader
}

func (h *Handler) AddToManager(ctx context.Context, mgr manager.Manager) error {
	if h.Cache == nil {
		h.Cache = mgr.GetCache()
	}
	if h.APIReader == nil {
		h.APIReader = mgr.GetAPIReader()
	}

	// Initialize caches here to ensure the readyz informer check will only succeed once informers required for this
	// handler have synced so that http requests can be served quicker with pre-syncronized caches.
	if _, err := h.Cache.GetInformer(ctx, &corev1.Namespace{}); err != nil {
		return err
	}
	if _, err := h.Cache.GetInformer(ctx, &gardencorev1beta1.Project{}); err != nil {
		return err
	}

	mgr.GetWebhookServer().Register(WebhookPath, &webhook.Admission{Handler: h})
	return nil
}

// InjectAPIReader injects a reader into the handler.
func (h *Handler) InjectAPIReader(reader client.Reader) error {
	h.APIReader = reader
	return nil
}

// Handle implements the webhook handler for namespace deletion validation.
func (h *Handler) Handle(ctx context.Context, request admission.Request) admission.Response {
	log := logf.FromContext(ctx)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// If the request does not indicate the correct operation (DELETE) we allow the review without further doing.
	if request.Operation != admissionv1.Delete {
		return acadmission.Allowed("operation is not DELETE")
	}
	if request.Kind != namespaceGVK {
		return acadmission.Allowed("resource is not corev1.Namespace")
	}
	if request.SubResource != "" {
		return acadmission.Allowed("subresources on namespaces are not handled")
	}

	// Now that all checks have been passed we can actually validate the admission request.
	reviewResponse := h.admitNamespace(ctx, request)
	if !reviewResponse.Allowed && reviewResponse.Result != nil {
		log.Info("Rejected namespace deletion", "message", reviewResponse.Result.Message)
	}
	return reviewResponse
}

// admitNamespace does only allow the request if no Shoots exist in this specific namespace anymore.
func (h *Handler) admitNamespace(ctx context.Context, request admission.Request) admission.Response {
	// Determine project for given namespace.
	// TODO: we should use a direct lookup here, as we might falsely allow the request, if our cache is
	// out of sync and doesn't know about the project. We should use a field selector for looking up the project
	// belonging to a given namespace.
	project, namespace, err := gutil.ProjectAndNamespaceFromReader(ctx, h.Cache, request.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if namespace == nil {
				return acadmission.Allowed("namespace is already gone")
			}
			return acadmission.Allowed("project for namespace not found")
		}
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if project == nil {
		return acadmission.Allowed("does not belong to a project")
	}

	switch {
	case namespace.DeletionTimestamp != nil:
		return acadmission.Allowed("namespace is already marked for deletion")
	case project.DeletionTimestamp != nil:
		// if project is marked for deletion we need to wait until all shoots in the namespace are gone
		namespaceInUse, err := kutil.IsNamespaceInUse(ctx, h.APIReader, namespace.Name, gardencorev1beta1.SchemeGroupVersion.WithKind("ShootList"))
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}

		if !namespaceInUse {
			return acadmission.Allowed("namespace doesn't contain any shoots")
		}

		return acadmission.Denied(fmt.Sprintf("deletion of namespace %q is not permitted (it still contains Shoots)", namespace.Name))
	}

	// Namespace is not yet marked for deletion and project is not marked as well. We do not admit and respond that
	// namespace deletion is only allowed via project deletion.
	return acadmission.Denied(fmt.Sprintf("direct deletion of namespace %q is not permitted (you must delete the corresponding project %q)", namespace.Name, project.Name))
}
