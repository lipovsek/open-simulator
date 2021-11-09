/*
Copyright © 2021 Alibaba Group Holding Ltd.

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

package v1alpha1

import (
	"context"
	"time"

	v1alpha1 "github.com/alibaba/open-local/pkg/apis/storage/v1alpha1"
	scheme "github.com/alibaba/open-local/pkg/generated/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// NodeLocalStoragesGetter has a method to return a NodeLocalStorageInterface.
// A group's client should implement this interface.
type NodeLocalStoragesGetter interface {
	NodeLocalStorages() NodeLocalStorageInterface
}

// NodeLocalStorageInterface has methods to work with NodeLocalStorage resources.
type NodeLocalStorageInterface interface {
	Create(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.CreateOptions) (*v1alpha1.NodeLocalStorage, error)
	Update(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.UpdateOptions) (*v1alpha1.NodeLocalStorage, error)
	UpdateStatus(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.UpdateOptions) (*v1alpha1.NodeLocalStorage, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1alpha1.NodeLocalStorage, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1alpha1.NodeLocalStorageList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.NodeLocalStorage, err error)
	NodeLocalStorageExpansion
}

// nodeLocalStorages implements NodeLocalStorageInterface
type nodeLocalStorages struct {
	client rest.Interface
}

// newNodeLocalStorages returns a NodeLocalStorages
func newNodeLocalStorages(c *CsiV1alpha1Client) *nodeLocalStorages {
	return &nodeLocalStorages{
		client: c.RESTClient(),
	}
}

// Get takes name of the nodeLocalStorage, and returns the corresponding nodeLocalStorage object, and an error if there is any.
func (c *nodeLocalStorages) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1alpha1.NodeLocalStorage, err error) {
	result = &v1alpha1.NodeLocalStorage{}
	err = c.client.Get().
		Resource("nodelocalstorages").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of NodeLocalStorages that match those selectors.
func (c *nodeLocalStorages) List(ctx context.Context, opts v1.ListOptions) (result *v1alpha1.NodeLocalStorageList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1alpha1.NodeLocalStorageList{}
	err = c.client.Get().
		Resource("nodelocalstorages").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested nodeLocalStorages.
func (c *nodeLocalStorages) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Resource("nodelocalstorages").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a nodeLocalStorage and creates it.  Returns the server's representation of the nodeLocalStorage, and an error, if there is any.
func (c *nodeLocalStorages) Create(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.CreateOptions) (result *v1alpha1.NodeLocalStorage, err error) {
	result = &v1alpha1.NodeLocalStorage{}
	err = c.client.Post().
		Resource("nodelocalstorages").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeLocalStorage).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a nodeLocalStorage and updates it. Returns the server's representation of the nodeLocalStorage, and an error, if there is any.
func (c *nodeLocalStorages) Update(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.UpdateOptions) (result *v1alpha1.NodeLocalStorage, err error) {
	result = &v1alpha1.NodeLocalStorage{}
	err = c.client.Put().
		Resource("nodelocalstorages").
		Name(nodeLocalStorage.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeLocalStorage).
		Do(ctx).
		Into(result)
	return
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *nodeLocalStorages) UpdateStatus(ctx context.Context, nodeLocalStorage *v1alpha1.NodeLocalStorage, opts v1.UpdateOptions) (result *v1alpha1.NodeLocalStorage, err error) {
	result = &v1alpha1.NodeLocalStorage{}
	err = c.client.Put().
		Resource("nodelocalstorages").
		Name(nodeLocalStorage.Name).
		SubResource("status").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(nodeLocalStorage).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the nodeLocalStorage and deletes it. Returns an error if one occurs.
func (c *nodeLocalStorages) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Resource("nodelocalstorages").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *nodeLocalStorages) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Resource("nodelocalstorages").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched nodeLocalStorage.
func (c *nodeLocalStorages) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1alpha1.NodeLocalStorage, err error) {
	result = &v1alpha1.NodeLocalStorage{}
	err = c.client.Patch(pt).
		Resource("nodelocalstorages").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}