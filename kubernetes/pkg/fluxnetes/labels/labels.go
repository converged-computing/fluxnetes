package labels

import (
	v1 "k8s.io/api/core/v1"
)

// Labels to be shared between different components

const (
	// We use the same label to be consistent
	// https://github.com/kubernetes-sigs/scheduler-plugins/blob/master/apis/scheduling/v1alpha1/types.go#L109
	PodGroupLabel     = "scheduling.x-k8s.io/pod-group"
	PodGroupSizeLabel = "fluxnetes.group-size"

	// Internal use (not used yet)
	PodGroupTimeCreated = "fluxnetes.created-at"
)

// GetPodGroupLabel get pod group name from pod labels
func GetPodGroupLabel(pod *v1.Pod) string {
	return pod.Labels[PodGroupLabel]
}
