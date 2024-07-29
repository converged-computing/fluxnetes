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
	"fmt"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1"

	// Our logger moved into Kubernetes
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/logger"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	corev1helpers "k8s.io/component-helpers/scheduling/corev1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	fcore "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/core"
	fgroup "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	flabel "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/labels"
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

// Fluxnetes schedules pods in a group using Fluxion as a backend
// We inherit cosched.Coscheduling to use some of the primary functions
type Fluxnetes struct {
	mutex           sync.Mutex
	handle          framework.Handle
	podGroupManager fcore.Manager
	scheduleTimeout *time.Duration
	podGroupBackoff *time.Duration
	log             *logger.DebugLogger
}

var (
	_ framework.QueueSortPlugin = &Fluxnetes{}
	_ framework.PreFilterPlugin = &Fluxnetes{}
	_ framework.FilterPlugin    = &Fluxnetes{}

	_ framework.PostFilterPlugin = &Fluxnetes{}
	_ framework.PermitPlugin     = &Fluxnetes{}
	_ framework.ReservePlugin    = &Fluxnetes{}

	_ framework.EnqueueExtensions = &Fluxnetes{}

	// Set to be the same as coscheduling
	permitWaitingTimeSeconds int64 = 300
	podGroupBackoffSeconds   int64 = 0
)

const (
	// Name is the name of the plugin used in Registry and configurations.
	Name = "Fluxnetes"
)

func (fluxnetes *Fluxnetes) Name() string {
	return Name
}

// Initialize and return a new Fluxnetes Scheduler Plugin
func New(_ context.Context, obj runtime.Object, handle framework.Handle) (framework.Plugin, error) {

	ctx := context.TODO()

	scheme := runtime.NewScheme()
	_ = clientscheme.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	kubeClient, err := client.New(handle.KubeConfig(), client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	// Make fluxnetes his own little logger!
	// This can eventually be a flag, but just going to set for now
	// It shall be a very chonky file. Oh lawd he comin!
	l := logger.NewDebugLogger(logger.LevelDebug, "/tmp/fluxnetes.log")

	// PermitWaitingTimeSeconds is the waiting timeout in seconds.
	scheduleTimeDuration := time.Duration(permitWaitingTimeSeconds) * time.Second

	// Performance improvement when retrieving list of objects by namespace or we'll log 'index not exist' warning.
	fluxPodsInformer := handle.SharedInformerFactory().Core().V1().Pods().Informer()
	fluxPodsInformer.AddIndexers(cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})

	podGroupManager := fcore.NewPodGroupManager(
		kubeClient,
		handle.SnapshotSharedLister(),
		&scheduleTimeDuration,
		// Keep the podInformer (from frameworkHandle) as the single source of Pods.
		handle.SharedInformerFactory().Core().V1().Pods(),
		l,
	)

	// Event handlers to call on podGroupManager
	fluxPodsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: podGroupManager.UpdatePod,
		DeleteFunc: podGroupManager.DeletePod,
	})
	go fluxPodsInformer.Run(ctx.Done())

	backoffSeconds := time.Duration(podGroupBackoffSeconds) * time.Second
	plugin := &Fluxnetes{
		handle:          handle,
		podGroupManager: podGroupManager,
		scheduleTimeout: &scheduleTimeDuration,
		log:             l,
		podGroupBackoff: &backoffSeconds,
	}

	// TODO this is not supported yet
	// Account for resources in running cluster
	err = plugin.RegisterExisting(ctx)
	return plugin, err
}

// Fluxnetes has added delete, although I wonder if update includes that signal
// and it's redundant?
func (fluxnetes *Fluxnetes) EventsToRegister() []framework.ClusterEventWithHint {
	// To register a custom event, follow the naming convention at:
	// https://git.k8s.io/kubernetes/pkg/scheduler/eventhandlers.go#L403-L410
	podGroupGVK := fmt.Sprintf("podgroups.v1alpha1.%v", GroupName)
	return []framework.ClusterEventWithHint{
		{Event: framework.ClusterEvent{Resource: framework.Pod, ActionType: framework.Add | framework.Delete}},
		{Event: framework.ClusterEvent{Resource: framework.GVK(podGroupGVK), ActionType: framework.Add | framework.Update | framework.Delete}},
	}
}

func (fluxnetes *Fluxnetes) Filter(
	ctx context.Context,
	cycleState *framework.CycleState,
	pod *corev1.Pod,
	nodeInfo *framework.NodeInfo,
) *framework.Status {

	fluxnetes.log.Verbose("[Fluxnetes Filter] Filtering input node %s", nodeInfo.Node().Name)
	state, err := cycleState.Read(framework.StateKey(pod.Name))

	// No error means we retrieved the state
	if err == nil {

		// Try to convert the state to FluxStateDate
		value, ok := state.(*fcore.FluxStateData)

		// If we have state data that isn't equal to the current assignment, no go
		if ok && value.NodeName != nodeInfo.Node().Name {
			return framework.NewStatus(framework.Unschedulable, "pod is not permitted")
		} else {
			fluxnetes.log.Info("[Fluxnetes Filter] node %s selected for %s\n", value.NodeName, pod.Name)
		}
	}
	return framework.NewStatus(framework.Success)
}

// Less is used to sort pods in the scheduling queue in the following order.
// 1. Compare the priorities of Pods.
// 2. Compare the initialization timestamps of PodGroups or Pods.
// 3. Compare the keys of PodGroups/Pods: <namespace>/<podname>.
func (fluxnetes *Fluxnetes) Less(podInfo1, podInfo2 *framework.QueuedPodInfo) bool {
	prio1 := corev1helpers.PodPriority(podInfo1.Pod)
	prio2 := corev1helpers.PodPriority(podInfo2.Pod)
	if prio1 != prio2 {
		return prio1 > prio2
	}

	// Important: this GetPodGroup returns the first name as the Namespaced one,
	// which is what fluxnetes needs to distinguish between namespaces. Just the
	// name could be replicated between different namespaces
	// TODO add some representation of PodGroup back
	ctx := context.TODO()
	name1, podGroup1 := fluxnetes.podGroupManager.GetPodGroup(ctx, podInfo1.Pod)
	name2, podGroup2 := fluxnetes.podGroupManager.GetPodGroup(ctx, podInfo2.Pod)

	// Fluxnetes can only compare if we have two known groups.
	// This tries for that first, and falls back to the initial attempt timestamp
	creationTime1 := fgroup.GetCreationTimestamp(name1, podGroup1, podInfo1)
	creationTime2 := fgroup.GetCreationTimestamp(name2, podGroup2, podInfo2)

	// If they are the same, fall back to sorting by name.
	if creationTime1.Equal(&creationTime2) {
		return fcore.GetNamespacedName(podInfo1.Pod) < fcore.GetNamespacedName(podInfo2.Pod)
	}
	return creationTime1.Before(&creationTime2)

}

// PreFilterExtensions allow for callbacks on filtered states
// This is required to be defined for a PreFilter plugin
// https://github.com/kubernetes/kubernetes/blob/master/pkg/scheduler/framework/interface.go#L383
func (fluxnetes *Fluxnetes) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

// PreFilter performs the following validations.
// 1. Whether the PodGroup that the Pod belongs to is on the deny list.
// 2. Whether the total number of pods in a PodGroup is less than its `minMember`.
func (fluxnetes *Fluxnetes) PreFilter(
	ctx context.Context,
	state *framework.CycleState,
	pod *corev1.Pod,
) (*framework.PreFilterResult, *framework.Status) {

	// Quick check if the pod is already scheduled
	fluxnetes.mutex.Lock()
	node := fluxnetes.podGroupManager.GetPodNode(pod)
	fluxnetes.mutex.Unlock()
	if node != "" {
		fluxnetes.log.Info("[Fluxnetes PreFilter] assigned pod %s to node %s\n", pod.Name, node)
		result := framework.PreFilterResult{NodeNames: sets.New(node)}
		return &result, framework.NewStatus(framework.Success, "")
	}
	fluxnetes.log.Info("[Fluxnetes PreFilter] pod %s does not have a node assigned\n", pod.Name)

	// This will populate the node name into the pod group manager
	err := fluxnetes.podGroupManager.PreFilter(ctx, pod, state)
	if err != nil {
		fluxnetes.log.Error("[Fluxnetes PreFilter] failed pod %s: %s", pod.Name, err.Error())
		return nil, framework.NewStatus(framework.UnschedulableAndUnresolvable, err.Error())
	}
	node = fluxnetes.podGroupManager.GetPodNode(pod)
	result := framework.PreFilterResult{NodeNames: sets.New(node)}
	return &result, framework.NewStatus(framework.Success, "")
}

// PostFilter is used to reject a group of pods if a pod does not pass PreFilter or Filter.
func (fluxnetes *Fluxnetes) PostFilter(
	ctx context.Context,
	state *framework.CycleState,
	pod *corev1.Pod,
	filteredNodeStatusMap framework.NodeToStatusMap,
) (*framework.PostFilterResult, *framework.Status) {

	groupName, podGroup := fluxnetes.podGroupManager.GetPodGroup(ctx, pod)
	if podGroup == nil {
		fluxnetes.log.Info("Pod does not belong to any group, pod %s", pod.Name)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable, "cannot find pod group")
	}

	// This explicitly checks nodes, and we can skip scheduling another pod if we already
	// have the minimum. For fluxnetes since we expect an exact size this likely is not needed
	assigned := fluxnetes.podGroupManager.CalculateAssignedPods(podGroup.Name, pod.Namespace)
	if assigned >= int(podGroup.Spec.MinMember) {
		fluxnetes.log.Info("Assigned pods podGroup %s is assigned %s", groupName, assigned)
		return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable)
	}

	// Took out percentage chcek here, doesn't make sense to me.

	// It's based on an implicit assumption: if the nth Pod failed,
	// it's inferrable other Pods belonging to the same PodGroup would be very likely to fail.
	fluxnetes.handle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
		if waitingPod.GetPod().Namespace == pod.Namespace && flabel.GetPodGroupLabel(waitingPod.GetPod()) == podGroup.Name {
			fluxnetes.log.Info("PostFilter rejects the pod for podGroup %s and pod %s", groupName, waitingPod.GetPod().Name)
			waitingPod.Reject(fluxnetes.Name(), "optimistic rejection in PostFilter")
		}
	})

	if fluxnetes.podGroupBackoff != nil {
		pods, err := fluxnetes.handle.SharedInformerFactory().Core().V1().Pods().Lister().Pods(pod.Namespace).List(
			labels.SelectorFromSet(labels.Set{v1alpha1.PodGroupLabel: flabel.GetPodGroupLabel(pod)}),
		)
		if err == nil && len(pods) >= int(podGroup.Spec.MinMember) {
			fluxnetes.podGroupManager.BackoffPodGroup(groupName, *fluxnetes.podGroupBackoff)
		}
	}

	fluxnetes.podGroupManager.DeletePermittedPodGroup(groupName)
	return &framework.PostFilterResult{}, framework.NewStatus(framework.Unschedulable,
		fmt.Sprintf("PodGroup %v gets rejected due to Pod %v is unschedulable even after PostFilter", groupName, pod.Name))
}

// Permit is the functions invoked by the framework at "Permit" extension point.
func (fluxnetes *Fluxnetes) Permit(
	ctx context.Context,
	state *framework.CycleState,
	pod *corev1.Pod,
	nodeName string,
) (*framework.Status, time.Duration) {

	fluxnetes.log.Info("Checking permit for pod %s to node %s", pod.Name, nodeName)
	waitTime := *fluxnetes.scheduleTimeout
	s := fluxnetes.podGroupManager.Permit(ctx, state, pod)
	var retStatus *framework.Status
	switch s {
	case fcore.PodGroupNotSpecified:
		fluxnetes.log.Info("Checking permit for pod %s to node %s: PodGroupNotSpecified", pod.Name, nodeName)
		return framework.NewStatus(framework.Success, ""), 0
	case fcore.PodGroupNotFound:
		fluxnetes.log.Info("Checking permit for pod %s to node %s: PodGroupNotFound", pod.Name, nodeName)
		return framework.NewStatus(framework.Unschedulable, "PodGroup not found"), 0
	case fcore.Wait:
		fluxnetes.log.Info("Pod %s is waiting to be scheduled to node %s", pod.Name, nodeName)
		_, podGroup := fluxnetes.podGroupManager.GetPodGroup(ctx, pod)
		if wait := fgroup.GetWaitTimeDuration(podGroup, fluxnetes.scheduleTimeout); wait != 0 {
			waitTime = wait
		}
		retStatus = framework.NewStatus(framework.Wait)

		// We will also request to move the sibling pods back to activeQ.
		fluxnetes.podGroupManager.ActivateSiblings(pod, state)
	case fcore.Success:
		podGroupFullName := flabel.GetPodGroupFullName(pod)
		fluxnetes.handle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
			if flabel.GetPodGroupFullName(waitingPod.GetPod()) == podGroupFullName {
				fluxnetes.log.Info("Permit allows pod %s", waitingPod.GetPod().Name)
				waitingPod.Allow(fluxnetes.Name())
			}
		})
		fluxnetes.log.Info("Permit allows pod %s", pod.Name)
		retStatus = framework.NewStatus(framework.Success)
		waitTime = 0
	}

	return retStatus, waitTime
}

// Reserve is the functions invoked by the framework at "reserve" extension point.
func (fluxnetes *Fluxnetes) Reserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	return nil
}

// Unreserve rejects all other Pods in the PodGroup when one of the pods in the group times out.
func (fluxnetes *Fluxnetes) Unreserve(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) {
	groupName, podGroup := fluxnetes.podGroupManager.GetPodGroup(ctx, pod)
	if podGroup == nil {
		return
	}
	fluxnetes.handle.IterateOverWaitingPods(func(waitingPod framework.WaitingPod) {
		if waitingPod.GetPod().Namespace == pod.Namespace && flabel.GetPodGroupLabel(waitingPod.GetPod()) == podGroup.Name {
			fluxnetes.log.Info("Unreserve rejects pod %s in group %s", waitingPod.GetPod().Name, groupName)
			waitingPod.Reject(fluxnetes.Name(), "rejection in Unreserve")
		}
	})
	fluxnetes.podGroupManager.DeletePermittedPodGroup(groupName)
}
