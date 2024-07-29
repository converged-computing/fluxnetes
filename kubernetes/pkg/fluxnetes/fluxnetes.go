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
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
)

var (
	GroupName = "scheduling.x-k8s.io"
)

// JobResult serializes a result from Fluxnetes in the scheduler back to metadata
type JobResult struct {
	JobID   int32  `json:"jobid"`
	Nodes   string `json:"nodes"`
	PodID   string `json:"podid"`
	PodSpec string `json:"podspec"`
}

func (j JobResult) GetNodes() []string {
	return strings.Split(j.Nodes, ",")
}

// Fluxnetes (as a plugin) is only enabled for the queue sort
type Fluxnetes struct{}

var (
	_ framework.QueueSortPlugin = &Fluxnetes{}
)

const (
	Name = "Fluxnetes"
)

func (fluxnetes *Fluxnetes) Name() string {
	return Name
}

// New returns an empty Fluxnetes plugin, which only provides a queue sort!
func New(_ context.Context, _ runtime.Object, _ framework.Handle) (framework.Plugin, error) {
	return &Fluxnetes{}, nil
}

// Less is used to sort pods in the scheduling queue in the following order.
// 1. Compare the priorities of Pods.
// 2. Compare the initialization timestamps of Pods.
// 3. Compare the keys of PodGroups/Pods: <namespace>/<podname>.
// In practice this step isn't hugely important because the Fluxnetes queue does
// more arranging of pods, but this helps to pre-sort.
func (fluxnetes *Fluxnetes) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	prio1 := helpers.PodPriority(podInfo1.Pod)
	prio2 := helpers.PodPriority(podInfo2.Pod)
	if prio1 != prio2 {
		return prio1 > prio2
	}

	// Important: this GetPodGroup returns the first name as the Namespaced one,
	// which is what fluxnetes needs to distinguish between namespaces. Just the
	// name could be replicated between different namespaces
	// TODO add some representation of PodGroup back
	name1 := groups.GetPodGroupName(podInfo1.Pod)
	name2 := groups.GetPodGroupName(podInfo2.Pod)

	// Try for creation time first, and fall back to naming
	creationTime1 := groups.GetPodCreationTimestamp(podInfo1.Pod)
	creationTime2 := groups.GetPodCreationTimestamp(podInfo2.Pod)

	// If they are the same, fall back to sorting by name.
	if creationTime1.Equal(&creationTime2) {
		return name1 < name2
	}
	return creationTime1.Before(&creationTime2)

}
