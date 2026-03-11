// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"context"
	"fmt"
	"net"

	dockerclient "github.com/docker/docker/client"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// AnnotationUseProxyLoadBalancer is the opt-in annotation for the proxy-based loadbalancer controller.
	AnnotationUseProxyLoadBalancer = "provider-local.gardener.cloud/use-proxy-loadbalancer"
	annotationLoadBalancerIP       = "provider-local.gardener.cloud/loadbalancer-ip"
	finalizerName                  = "provider-local.gardener.cloud/loadbalancer"
)

// Reconciler reconciles LoadBalancer services using Docker proxy containers with Envoy.
type Reconciler struct {
	Client      client.Client
	Docker      *dockerclient.Client
	IPAllocator *IPAllocator
	Network     string
	EnvoyImage  string
}

// Reconcile reconciles a Service.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	service := &corev1.Service{}
	if err := r.Client.Get(ctx, req.NamespacedName, service); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("error retrieving object from store: %w", err)
	}

	serviceKey := fmt.Sprintf("%s/%s", service.Namespace, service.Name)

	if service.DeletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(service, finalizerName) {
			log.Info("Cleaning up proxy container for deleted service")
			if err := deleteContainer(ctx, log, r.Docker, serviceKey); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to delete proxy container: %w", err)
			}
			r.IPAllocator.Release(serviceKey)

			patch := client.MergeFrom(service.DeepCopy())
			controllerutil.RemoveFinalizer(service, finalizerName)
			if err := r.Client.Patch(ctx, service, patch); err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return reconcile.Result{}, nil
	}

	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return reconcile.Result{}, nil
	}
	if service.Annotations[AnnotationUseProxyLoadBalancer] != "true" {
		return reconcile.Result{}, nil
	}

	log.Info("Reconciling LoadBalancer service")

	if !controllerutil.ContainsFinalizer(service, finalizerName) {
		patch := client.MergeFrom(service.DeepCopy())
		controllerutil.AddFinalizer(service, finalizerName)
		if err := r.Client.Patch(ctx, service, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
	}

	desiredIP := service.Annotations[annotationLoadBalancerIP]
	lbIP, err := r.IPAllocator.Allocate(serviceKey, desiredIP)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to allocate IP for service %s: %w", serviceKey, err)
	}
	lbIPStr := lbIP.String()

	if desiredIP != lbIPStr {
		patch := client.MergeFrom(service.DeepCopy())
		if service.Annotations == nil {
			service.Annotations = make(map[string]string)
		}
		service.Annotations[annotationLoadBalancerIP] = lbIPStr
		if err := r.Client.Patch(ctx, service, patch); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to set IP annotation: %w", err)
		}
	}

	cName, err := ensureContainer(ctx, log, r.Docker, service, lbIPStr, r.Network, r.EnvoyImage)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to ensure proxy container: %w", err)
	}

	nodeIPs, err := r.getNodeInternalIPs(ctx)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to get node IPs: %w", err)
	}

	ldsConfig, cdsConfig, err := generateProxyConfig(service, nodeIPs)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to generate proxy config: %w", err)
	}

	if err := updateContainerConfig(ctx, log, r.Docker, cName, ldsConfig, cdsConfig); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update container config: %w", err)
	}

	patch := client.MergeFrom(service.DeepCopy())
	service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: lbIPStr}}
	if err := r.Client.Status().Patch(ctx, service, patch); err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to update service status: %w", err)
	}

	log.Info("Successfully reconciled LoadBalancer service", "ip", lbIPStr, "container", cName)
	return reconcile.Result{}, nil
}

func (r *Reconciler) getNodeInternalIPs(ctx context.Context) ([]string, error) {
	nodeList := &corev1.NodeList{}
	if err := r.Client.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("error listing nodes: %w", err)
	}

	var ips []string
	for _, node := range nodeList.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				if ip := net.ParseIP(addr.Address); ip != nil && ip.To4() != nil {
					ips = append(ips, addr.Address)
				}
			}
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no nodes with internal IPs found")
	}

	return ips, nil
}
