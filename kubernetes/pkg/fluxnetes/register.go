/*
Copyright 2020 The Kubernetes Authors.

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

package fluxnetes

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// RegisterExisting uses the in cluster API to ensure existing pods
// are known to fluxnetes, This is a one-time, static approach, so if a resource
// here goes away we cannot remove it from being known. But it's better than
// not having it, and having fluxion assume more resources than the
// cluster has available. This is a TODO as fluxion does not support it
func (fluxnetes *Fluxnetes) RegisterExisting(ctx context.Context) error {

	// creates an in-cluster config and client
	config, err := rest.InClusterConfig()
	if err != nil {
		fluxnetes.log.Error("[Fluxnetes RegisterExisting] Error creating in-cluster config: %s\n", err)
		return err
	}
	// creates the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fluxnetes.log.Error("[Fluxnetes RegisterExisting] Error creating client for config: %s\n", err)
		return err
	}
	// get pods in all the namespaces by omitting namespace
	// Or specify namespace to get pods in particular namespace
	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		fluxnetes.log.Info("[Fluxnetes RegisterExisting] Error listing pods: %s\n", err)
		return err
	}
	fluxnetes.log.Info("[Fluxnetes RegisterExisting] Found %d existing pods in the cluster\n", len(pods.Items))
	return nil
}
