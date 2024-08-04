package labels

import (
	v1 "k8s.io/api/core/v1"
)

// Labels to be shared between different components

const (
	// We use the same label to be consistent
	PodGroupLabel     = "fluxnetes.group-name"
	PodGroupSizeLabel = "fluxnetes.group-size"
)

// GetPodGroupLabel get pod group name from pod labels
func GetPodGroupLabel(pod *v1.Pod) string {
	return pod.Labels[PodGroupLabel]
}
