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

package shoot

import (
	"context"
	"time"

	"github.com/gardener/gardener/pkg/utils/flow"
)

type Controller struct {
}

// runReconcileShootFlow reconciles the Shoot cluster's state.
// It receives an Operation object <o> which stores the Shoot object.
// +gardener:flow-viz-gen="Shoot Reconcile"
func runReconcileShootFlow() {
	// We create the botanists (which will do the actual work).
	var (
		defaultTimeout  = 30 * time.Second
		defaultInterval = 5 * time.Second

		g                         = flow.NewGraph("Shoot cluster reconciliation")
		syncClusterResourceToSeed = g.Add(flow.Task{
			Name: "Syncing shoot cluster information to seed",
			Fn:   flow.TaskFn(Foo).RetryUntilTimeout(defaultInterval, defaultTimeout),
		})
		_ = g.Add(flow.Task{
			Name: "Ensuring that ShootState exists",
			Fn:   flow.TaskFn(Foo).RetryUntilTimeout(defaultInterval, defaultTimeout),
		})
		deployNamespace = g.Add(flow.Task{
			Name:         "Deploying Shoot namespace in Seed",
			Fn:           flow.TaskFn(Foo).RetryUntilTimeout(defaultInterval, defaultTimeout),
			Dependencies: flow.NewTaskIDs(syncClusterResourceToSeed),
		})
		_ = g.Add(flow.Task{
			Name:         "Deploying cloud provider account secret",
			Fn:           flow.TaskFn(Foo).RetryUntilTimeout(defaultInterval, defaultTimeout),
			Dependencies: flow.NewTaskIDs(deployNamespace),
		})
		syncPointReadyForCleanup = flow.NewTaskIDs(
			syncClusterResourceToSeed,
			deployNamespace,
		)
		deployKubeAPIServerService = g.Add(flow.Task{
			Name:         "Deploying Kubernetes API server service in the Seed cluster",
			Fn:           flow.TaskFn(Foo).RetryUntilTimeout(defaultInterval, defaultTimeout).SkipIf(false),
			Dependencies: flow.NewTaskIDs(syncPointReadyForCleanup),
		})
		_ = g.Add(flow.Task{
			Name:         "Waiting until Kubernetes API server service in the Seed cluster has reported readiness",
			Fn:           flow.TaskFn(Foo).SkipIf(false),
			Dependencies: flow.NewTaskIDs(deployKubeAPIServerService),
		})
		f = g.Compile()
	)

	if err := f.Run(flow.Opts{}); err != nil {
		return
	}

	return
}

func Foo(ctx context.Context) error {
	return nil
}
