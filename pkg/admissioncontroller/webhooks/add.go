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

package webhooks

import (
	"context"
	"fmt"

	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gardener/gardener/pkg/admissioncontroller/apis/config"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/auditpolicy"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/internaldomainsecret"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/kubeconfigsecret"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/namespacedeletion"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/resourcesize"
	"github.com/gardener/gardener/pkg/admissioncontroller/webhooks/admission/seedrestriction"
	seedauthorizer "github.com/gardener/gardener/pkg/admissioncontroller/webhooks/auth/seed"
)

// AddWebhooksToManager adds all admission-controller webhooks to the given manager.
func AddWebhooksToManager(ctx context.Context, mgr manager.Manager, cfg *config.AdmissionControllerConfiguration) error {
	if err := (&seedauthorizer.Authorizer{
		EnableDebugHandlers: pointer.BoolDeref(cfg.Server.EnableDebugHandlers, false),
	}).AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed adding seedauthorizer handler: %w", err)
	}

	if err := (&seedrestriction.Handler{}).AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed adding seedrestriction handler: %w", err)
	}

	if err := (&namespacedeletion.Handler{}).AddToManager(ctx, mgr); err != nil {
		return fmt.Errorf("failed adding namespacedeletion handler: %w", err)
	}

	if err := (&kubeconfigsecret.Handler{}).AddToManager(mgr); err != nil {
		return fmt.Errorf("failed adding kubeconfigsecret handler: %w", err)
	}

	if err := (&resourcesize.Handler{
		Config: *cfg.Server.ResourceAdmissionConfiguration,
	}).AddToManager(mgr); err != nil {
		return fmt.Errorf("failed adding resourcesize handler: %w", err)
	}

	if err := (&auditpolicy.Handler{}).AddToManager(mgr); err != nil {
		return fmt.Errorf("failed adding auditpolicy handler: %w", err)
	}

	if err := (&internaldomainsecret.Handler{}).AddToManager(mgr); err != nil {
		return fmt.Errorf("failed adding internaldomainsecret handler: %w", err)
	}

	return nil
}
