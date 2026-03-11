// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"io"

	cloudprovider "k8s.io/cloud-provider"
)

// Name is the name of the local cloud provider as specified in the cloud controller manager's --cloud-provider flag.
const Name = "local"

// Register registers the cloud provider implementation for the cloud-controller-manager.
// Other implementations typically call this function from their init() function, requiring an anyonymous import of this
// package in the main package of the cloud-controller-manager. We take a more explicit approach here by calling this
// function directly from the main package.
func Register() {
	cloudprovider.RegisterCloudProvider(Name, func(config io.Reader) (cloudprovider.Interface, error) {
		// TODO(timebertt): read config file
		// TODO(timebertt): instantiate docker client and pass it to the cloud provider implementation
		return New()
	})
}

// New returns a new instance of the local cloud provider implementation.
func New() (cloudprovider.Interface, error) {
	return &Local{}, nil
}

type Local struct {
}

func (l *Local) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) Instances() (cloudprovider.Instances, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) InstancesV2() (cloudprovider.InstancesV2, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) Zones() (cloudprovider.Zones, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) Clusters() (cloudprovider.Clusters, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) Routes() (cloudprovider.Routes, bool) {
	// TODO implement me
	panic("implement me")
}

func (l *Local) ProviderName() string {
	return Name
}

func (l *Local) HasClusterID() bool {
	// TODO implement me
	panic("implement me")
}
