package group

import (
	"context"
	"fmt"
	"time"

	"strconv"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/labels"
)

// A PodGroup holds the name and size of a pod group
// It is just a temporary holding structure
type PodGroup struct {
	Name      string
	Size      int32
	Timestamp metav1.MicroTime
	Duration  int64
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

// GetPodGroupFullName get namespaced group name from pod labels
// This is primarily for sorting, so we consider namespace too.
func GetPodGroupFullName(pod *corev1.Pod) string {
	groupName := GetPodGroupName(pod)
	return fmt.Sprintf("%v/%v", pod.Namespace, groupName)
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

// AddDuration adds the pod.Spec.ActiveDeadlineSeconds if it isn't set.
func AddDeadline(ctx context.Context, pod *corev1.Pod) error {

	// Cut out early if it is nil - will be added later
	if pod.Spec.ActiveDeadlineSeconds == nil {
		return nil
	}
	// Also cut out early with no error if one is set
	if *pod.Spec.ActiveDeadlineSeconds > int64(0) {
		return nil
	}
	payload := `{"spec": {"activeDeadlineSeconds": 3600}`
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	_, err = clientset.CoreV1().Pods(pod.Namespace).Patch(ctx, pod.Name, types.MergePatchType, []byte(payload), metav1.PatchOptions{})
	return err
}

// GetPodGroupDuration gets the runtime of a job in seconds
// We default to an hour (3600 seconds)
func GetPodGroupDuration(pod *corev1.Pod) (int64, error) {

	// It is already set
	if pod.Spec.ActiveDeadlineSeconds != nil && *pod.Spec.ActiveDeadlineSeconds > int64(0) {
		return *pod.Spec.ActiveDeadlineSeconds, nil
	}
	// We can't enforce everything have a duration, lots of services should not.
	return 0, nil
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
