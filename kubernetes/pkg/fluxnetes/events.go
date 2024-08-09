package fluxnetes

import (
	"encoding/json"

	podutil "k8s.io/kubernetes/pkg/api/v1/pod"

	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"

	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"
)

// Cleanup deletes a pod. It is assumed that it cannot be scheduled
// This means we do not have a flux id to cancel (-1)
func (q Queue) Cleanup(pod *corev1.Pod, podspec, groupName string) error {
	return workers.Cleanup(q.Context, podspec, int64(-1), true, groupName)
}

// UpdatePodEvent is called on an update, and the old and new object are presented
func (q *Queue) UpdatePodEvent(oldObj, newObj interface{}) {

	pod := oldObj.(*corev1.Pod)
	newPod := newObj.(*corev1.Pod)

	// a pod is updated, get the group. TODO: how to handle change in group name?
	// groupName := groups.GetPodGroupName(oldPod)
	switch pod.Status.Phase {
	case corev1.PodPending:
		klog.Infof("Received update event 'Pending' to '%s' for pod %s/%s", newPod.Status.Phase, pod.Namespace, pod.Name)
	case corev1.PodRunning:
		klog.Infof("Received update event 'Running' to '%s' for pod %s/%s", newPod.Status.Phase, pod.Namespace, pod.Name)
	case corev1.PodSucceeded:
		klog.Infof("Received update event 'Succeeded' to '%s' for pod %s/%s", newPod.Status.Phase, pod.Namespace, pod.Name)
	case corev1.PodFailed:
		klog.Infof("Received update event 'Failed' to '%s' for pod %s/%s", newPod.Status.Phase, pod.Namespace, pod.Name)
	case corev1.PodUnknown:
		klog.Infof("Received update event 'Unknown' to '%s' for pod %s/%s", newPod.Status.Phase, pod.Namespace, pod.Name)
	default:
		klog.Infof("Received unknown update event %s for pod %s/%s", newPod.Status.Phase, pod.Status.Phase, pod.Namespace, pod.Name)
	}
}

// DeletePodEventhandles the delete event handler
// We don't need to worry about calling cancel to fluxion if the fluxid is already cleaned up
// It has a boolean that won't return an error if the job does not exist.
func (q *Queue) DeletePodEvent(podObj interface{}) {
	pod := podObj.(*corev1.Pod)

	switch pod.Status.Phase {
	case corev1.PodPending:
		klog.Infof("Received delete event 'Pending' for pod %s/%s", pod.Namespace, pod.Name)
	case corev1.PodRunning:
		klog.Infof("Received delete event 'Running' for pod %s/%s", pod.Namespace, pod.Name)
	case corev1.PodSucceeded:
		klog.Infof("Received delete event 'Succeeded' for pod %s/%s", pod.Namespace, pod.Name)
	case corev1.PodFailed:
		klog.Infof("Received delete event 'Failed' for pod %s/%s", pod.Namespace, pod.Name)
	case corev1.PodUnknown:
		klog.Infof("Received delete event 'Unknown' for pod %s/%s", pod.Namespace, pod.Name)
	default:
		klog.Infof("Received unknown update event %s for pod %s/%s", pod.Status.Phase, pod.Namespace, pod.Name)
	}
	// Get the fluxid from the database, and issue cleanup for the group:
	// - deletes fluxID if it exists
	// - cleans up Kubernetes objects up to parent with "true"
	// - cleans up job in pending table
	podspec, err := json.Marshal(pod)
	if err != nil {
		klog.Errorf("Issue marshalling podspec for Pod %s/%s", pod.Namespace, pod.Name)
	}
	groupName := groups.GetPodGroupName(pod)

	// Since this is a termination event (meaning a single pod has terminated)
	// we only want to cancel the fluxion job if ALL pods in the group are done.
	// We don't want to delete the Kubernetes objects - this should happen on its
	// own, never (if no timeout) or with a cancel job if a duration is set
	pods, err := q.GetGroupPods(pod.Namespace, groupName)
	if err != nil {
		klog.Errorf("Issue getting group pods for %s", groupName)
	}
	fluxID, err := q.GetFluxID(pod.Namespace, groupName)

	// Determine finished status (delete via fluxion flux id ONLY if all are finished)
	finished := true
	for _, pod := range pods {
		isFinished := podutil.IsPodPhaseTerminal(pod.Status.Phase)
		if !isFinished {
			finished = false
			break
		}
	}
	if !finished {
		fluxID = -1
	}
	err = workers.Cleanup(q.Context, string(podspec), fluxID, false, groupName)
}
