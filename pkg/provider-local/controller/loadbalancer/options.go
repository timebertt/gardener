// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package loadbalancer

import (
	"github.com/spf13/pflag"

	"github.com/gardener/gardener/extensions/pkg/controller/cmd"
)

// ControllerOptions are command line options for the loadbalancer controller.
type ControllerOptions struct {
	MaxConcurrentReconciles int
	Network                 string
	EnvoyImage              string

	config *ControllerConfig
}

// AddFlags implements Flagger.AddFlags.
func (c *ControllerOptions) AddFlags(fs *pflag.FlagSet) {
	fs.IntVar(&c.MaxConcurrentReconciles, cmd.MaxConcurrentReconcilesFlag, c.MaxConcurrentReconciles, "The maximum number of concurrent reconciliations.")
	fs.StringVar(&c.Network, "network", c.Network, "Docker network to attach proxy containers to")
	fs.StringVar(&c.EnvoyImage, "envoy-image", c.EnvoyImage, "Envoy Docker image for proxy containers")
}

// Complete implements Completer.Complete.
func (c *ControllerOptions) Complete() error {
	c.config = &ControllerConfig{
		MaxConcurrentReconciles: c.MaxConcurrentReconciles,
		Network:                 c.Network,
		EnvoyImage:              c.EnvoyImage,
	}
	return nil
}

// Completed returns the completed ControllerConfig. Only call this if `Complete` was successful.
func (c *ControllerOptions) Completed() *ControllerConfig {
	return c.config
}

// ControllerConfig is a completed controller configuration.
type ControllerConfig struct {
	MaxConcurrentReconciles int
	Network                 string
	EnvoyImage              string
}

// Apply sets the values of this ControllerConfig in the given AddOptions.
func (c *ControllerConfig) Apply(opts *AddOptions) {
	opts.Controller.MaxConcurrentReconciles = c.MaxConcurrentReconciles
	opts.Network = c.Network
	opts.EnvoyImage = c.EnvoyImage
}
