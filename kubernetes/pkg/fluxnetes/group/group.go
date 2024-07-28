package group

import (
	"fmt"
	"time"

	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klog "k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/labels"
	"sigs.k8s.io/scheduler-plugins/apis/scheduling/v1alpha1"
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

// TODO(vsoch) delete everything below here when PodGroup is no longer used
// DefaultWaitTime is 60s if ScheduleTimeoutSeconds is not specified.
const DefaultWaitTime = 60 * time.Second

// CreateFakeGroup wraps an arbitrary pod in a fake group for fluxnetes to schedule
// This happens only in PreFilter so we already sorted
func CreateFakeGroup(pod *corev1.Pod) *v1alpha1.PodGroup {
	groupName := fmt.Sprintf("fluxnetes-solo-%s-%s", pod.Namespace, pod.Name)
	return &v1alpha1.PodGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      groupName,
			Namespace: pod.Namespace,
		},
		Spec: v1alpha1.PodGroupSpec{MinMember: int32(1)},
	}
}

// GetCreationTimestamp first tries the group, then falls back to the initial attempt timestamp
// This is the only update we have made to the upstream PodGroupManager, because we are expecting
// a MicroTime and not a time.Time.
func GetCreationTimestamp(groupName string, podGroup *v1alpha1.PodGroup, podInfo *framework.QueuedPodInfo) metav1.MicroTime {

	// Don't try to get a time for a pod group that does not exist
	if podGroup == nil {
		return metav1.NewMicroTime(*podInfo.InitialAttemptTimestamp)
	}

	// IsZero is an indicator if this was actually set
	// If the group label was present and we have a group, this will be true
	if !podGroup.Status.ScheduleStartTime.IsZero() {
		klog.Infof("   [Fluxnetes] Pod group %s was created at %s\n", groupName, podGroup.Status.ScheduleStartTime)
		return metav1.NewMicroTime(podGroup.Status.ScheduleStartTime.Time)
	}
	// We should actually never get here.
	klog.Errorf("   [Fluxnetes] Pod group %s time IsZero, we should not have reached here", groupName)
	return metav1.NewMicroTime(*podInfo.InitialAttemptTimestamp)
}

// GetWaitTimeDuration returns a wait timeout based on the following precedences:
// 1. spec.scheduleTimeoutSeconds of the given podGroup, if specified
// 2. given scheduleTimeout, if not nil
// 3. fall back to DefaultWaitTime
func GetWaitTimeDuration(podGroup *v1alpha1.PodGroup, scheduleTimeout *time.Duration) time.Duration {
	if podGroup != nil && podGroup.Spec.ScheduleTimeoutSeconds != nil {
		return time.Duration(*podGroup.Spec.ScheduleTimeoutSeconds) * time.Second
	}
	if scheduleTimeout != nil && *scheduleTimeout != 0 {
		return *scheduleTimeout
	}
	return DefaultWaitTime
}
