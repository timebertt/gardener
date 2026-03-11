// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"context"
	"fmt"
	"net/netip"

	dockerclient "github.com/docker/docker/client"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// ControllerName is the name of this controller.
	ControllerName    = "loadbalancer"
	DefaultCIDR       = "172.18.255.200/24"
	DefaultNetwork    = "kind"
	DefaultEnvoyImage = "envoyproxy/envoy:v1.33.2"
)

// DefaultAddOptions are the default AddOptions for AddToManager.
var DefaultAddOptions = AddOptions{}

// AddOptions are options to apply when adding the loadbalancer controller to the manager.
type AddOptions struct {
	Controller controller.Options
	Network    string
	EnvoyImage string
}

// AddToManager adds a controller with the default Options.
func AddToManager(ctx context.Context, mgr manager.Manager) error {
	return AddToManagerWithOptions(ctx, mgr, DefaultAddOptions)
}

// AddToManagerWithOptions adds a controller with the given Options.
func AddToManagerWithOptions(_ context.Context, mgr manager.Manager, opts AddOptions) error {
	allocator, err := NewIPAllocator(DefaultCIDR, []netip.Addr{netip.MustParseAddr("172.18.255.53")})
	if err != nil {
		return fmt.Errorf("failed to create IP allocator: %w", err)
	}

	docker, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("failed to create Docker client: %w", err)
	}

	reconciler := &Reconciler{
		Client:      mgr.GetClient(),
		Docker:      docker,
		IPAllocator: allocator,
		Network:     opts.Network,
		EnvoyImage:  opts.EnvoyImage,
	}

	// Restore existing IP allocations and GC orphaned containers after the cache starts.
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		log := logf.FromContext(ctx).WithName("loadbalancer-restore")
		if err := restoreAllocations(ctx, log, mgr.GetClient(), docker, allocator); err != nil {
			log.Error(err, "Failed to restore existing IP allocations")
		}
		return nil
	})); err != nil {
		return fmt.Errorf("failed to add restore runnable: %w", err)
	}

	return builder.
		ControllerManagedBy(mgr).
		Named(ControllerName).
		For(&corev1.Service{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			svc, ok := obj.(*corev1.Service)
			if !ok {
				return false
			}
			return svc.Spec.Type == corev1.ServiceTypeLoadBalancer &&
				svc.Annotations[AnnotationUseProxyLoadBalancer] == "true"
		}))).
		WithOptions(opts.Controller).
		Complete(reconciler)
}

func restoreAllocations(ctx context.Context, log logr.Logger, c client.Client, docker *dockerclient.Client, allocator *IPAllocator) error {
	serviceList := &corev1.ServiceList{}
	if err := c.List(ctx, serviceList); err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	serviceKeys := make(map[string]bool)
	for i := range serviceList.Items {
		svc := &serviceList.Items[i]
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer || svc.Annotations[AnnotationUseProxyLoadBalancer] != "true" {
			continue
		}

		serviceKey := fmt.Sprintf("%s/%s", svc.Namespace, svc.Name)
		serviceKeys[serviceKey] = true

		if ipStr := svc.Annotations[annotationLoadBalancerIP]; ipStr != "" {
			if ip, err := netip.ParseAddr(ipStr); err == nil {
				if err := allocator.Restore(serviceKey, ip); err != nil {
					log.Error(err, "Failed to restore IP allocation", "service", serviceKey, "ip", ipStr)
				} else {
					log.Info("Restored IP allocation", "service", serviceKey, "ip", ipStr)
				}
			}
		}
	}

	if err := garbageCollectContainers(ctx, log, docker, serviceKeys); err != nil {
		log.Error(err, "Failed to garbage collect orphaned containers")
	}

	return nil
}
