package group

import (
	"fmt"
	"time"

	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/labels"
)

// A PodGroup holds the name and size of a pod group
// It is just a temporary holding structure
type PodGroup struct {
	Name      string
	Size      int32
	Timestamp metav1.MicroTime
}

// getPodGroupName returns the pod group name
// 1. We first look to see if the pod is explicitly labeled
// 2. If not, we fall back to a default based on the pod name and namespace
func GetPodGroupName(pod *corev1.Pod) string {
	groupName := labels.GetPodGroupLabel(pod)

	// If we don't have a group, create one under fluxnetes namespace
	if groupName == "" {
		groupName = fmt.Sprintf("fluxnetes-group-%s-%s", pod.Namespace, pod.Name)
	}
	return groupName
}

// getPodGroupSize gets the group size, first from label then default of 1
func GetPodGroupSize(pod *corev1.Pod) (int32, error) {

	// Do we have a group size? This will be parsed as a string, likely
	groupSize, ok := pod.Labels[labels.PodGroupSizeLabel]
	if !ok {
		groupSize = "1"
		pod.Labels[labels.PodGroupSizeLabel] = groupSize
	}

	// We need the group size to be an integer now!
	size, err := strconv.ParseInt(groupSize, 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(size), nil
}

// GetPodCreationTimestamp
func GetPodCreationTimestamp(pod *corev1.Pod) metav1.MicroTime {

	// This is the first member of the group - use its CreationTimestamp
	if !pod.CreationTimestamp.IsZero() {
		return metav1.NewMicroTime(pod.CreationTimestamp.Time)
	}
	// If the pod for some reasond doesn't have a timestamp, assume now
	return metav1.NewMicroTime(time.Now())
}
