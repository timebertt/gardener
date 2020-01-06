/*
Copyright (c) 2020 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by client-gen. DO NOT EDIT.

package internalversion

import (
	"time"

	core "github.com/gardener/gardener/pkg/apis/core"
	scheme "github.com/gardener/gardener/pkg/client/core/clientset/internalversion/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// BackupEntriesGetter has a method to return a BackupEntryInterface.
// A group's client should implement this interface.
type BackupEntriesGetter interface {
	BackupEntries(namespace string) BackupEntryInterface
}

// BackupEntryInterface has methods to work with BackupEntry resources.
type BackupEntryInterface interface {
	Create(*core.BackupEntry) (*core.BackupEntry, error)
	Update(*core.BackupEntry) (*core.BackupEntry, error)
	UpdateStatus(*core.BackupEntry) (*core.BackupEntry, error)
	Delete(name string, options *v1.DeleteOptions) error
	DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error
	Get(name string, options v1.GetOptions) (*core.BackupEntry, error)
	List(opts v1.ListOptions) (*core.BackupEntryList, error)
	Watch(opts v1.ListOptions) (watch.Interface, error)
	Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *core.BackupEntry, err error)
	BackupEntryExpansion
}

// backupEntries implements BackupEntryInterface
type backupEntries struct {
	client rest.Interface
	ns     string
}

// newBackupEntries returns a BackupEntries
func newBackupEntries(c *CoreClient, namespace string) *backupEntries {
	return &backupEntries{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the backupEntry, and returns the corresponding backupEntry object, and an error if there is any.
func (c *backupEntries) Get(name string, options v1.GetOptions) (result *core.BackupEntry, err error) {
	result = &core.BackupEntry{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("backupentries").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of BackupEntries that match those selectors.
func (c *backupEntries) List(opts v1.ListOptions) (result *core.BackupEntryList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &core.BackupEntryList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("backupentries").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested backupEntries.
func (c *backupEntries) Watch(opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("backupentries").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch()
}

// Create takes the representation of a backupEntry and creates it.  Returns the server's representation of the backupEntry, and an error, if there is any.
func (c *backupEntries) Create(backupEntry *core.BackupEntry) (result *core.BackupEntry, err error) {
	result = &core.BackupEntry{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("backupentries").
		Body(backupEntry).
		Do().
		Into(result)
	return
}

// Update takes the representation of a backupEntry and updates it. Returns the server's representation of the backupEntry, and an error, if there is any.
func (c *backupEntries) Update(backupEntry *core.BackupEntry) (result *core.BackupEntry, err error) {
	result = &core.BackupEntry{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("backupentries").
		Name(backupEntry.Name).
		Body(backupEntry).
		Do().
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().

func (c *backupEntries) UpdateStatus(backupEntry *core.BackupEntry) (result *core.BackupEntry, err error) {
	result = &core.BackupEntry{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("backupentries").
		Name(backupEntry.Name).
		SubResource("status").
		Body(backupEntry).
		Do().
		Into(result)
	return
}

// Delete takes name of the backupEntry and deletes it. Returns an error if one occurs.
func (c *backupEntries) Delete(name string, options *v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("backupentries").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *backupEntries) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	var timeout time.Duration
	if listOptions.TimeoutSeconds != nil {
		timeout = time.Duration(*listOptions.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("backupentries").
		VersionedParams(&listOptions, scheme.ParameterCodec).
		Timeout(timeout).
		Body(options).
		Do().
		Error()
}

// Patch applies the patch and returns the patched backupEntry.
func (c *backupEntries) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *core.BackupEntry, err error) {
	result = &core.BackupEntry{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("backupentries").
		SubResource(subresources...).
		Name(name).
		Body(data).
		Do().
		Into(result)
	return
}
