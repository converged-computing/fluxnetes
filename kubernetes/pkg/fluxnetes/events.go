package fluxnetes

import (
	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"
)

// UpdatePod is called on an update, and the old and new object are presented
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

// DeletePod handles the delete event handler
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

}
