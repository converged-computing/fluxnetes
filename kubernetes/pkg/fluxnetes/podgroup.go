package fluxnetes

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"context"
	"fmt"

	klog "k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/labels"
)

// Queries
const (
	getTimestampQuery = "select created_at from pods_provisional where group_name=$1 limit 1"
	getPodQuery       = "select * from pods_provisional where group_name=$1 and namespace=$2 and name=$3"
	insertPodQuery    = "insert into pods_provisional (podspec, namespace, name, created_at, group_name, group_size) values ($1, $2, $3, $4, $5, $6)"
)

// A PodGroup holds the name and size of a pod group
// It is just a temporary holding structure
type PodGroup struct {
	Name      string
	Size      int32
	Timestamp metav1.MicroTime
}

// GetPodGroup returns the PodGroup that a Pod belongs to in cache.
func (q *Queue) GetPodGroup(pod *corev1.Pod) (*PodGroup, error) {
	groupName := labels.GetPodGroupLabel(pod)

	// If we don't have a group, create one under fluxnetes namespace
	if groupName == "" {
		groupName = fmt.Sprintf("fluxnetes-group-%s", pod.Name)
	}

	// Do we have a group size? This will be parsed as a string, likely
	groupSize, ok := pod.Labels[labels.PodGroupSizeLabel]
	if !ok {
		groupSize = "1"
		pod.Labels[labels.PodGroupSizeLabel] = groupSize
	}

	// Log the namespace/name, group name, and size
	klog.Infof("Pod Group Name for %s is %s (%s)", pod.Name, groupName, groupSize)

	// Get the creation timestamp for the group
	ts, err := q.GetCreationTimestamp(pod, groupName)
	if err != nil {
		return nil, err
	}

	// We need the group size to be an integer now!
	size, err := strconv.ParseInt(groupSize, 10, 32)
	if err != nil {
		return nil, err
	}

	// This can be improved - only get once for the group
	// and add to some single table
	return &PodGroup{Size: int32(size), Name: groupName, Timestamp: ts}, nil
}

// EnqueuePod checks to see if a specific pod exists for a group. If yes, do nothing.
// If not, add it. If yes, update the podspec.
func (q *Queue) EnqueuePod(ctx context.Context, pod *corev1.Pod, group *PodGroup) error {

	// This query will fail if there are no rows (the podGroup is not known)
	var groupName, name, namespace string
	row := q.Pool.QueryRow(context.Background(), getPodQuery, group.Name, pod.Namespace, pod.Name)
	err := row.Scan(groupName, name, namespace)
	if err == nil {

		// TODO vsoch: if we can update a podspec, need to retrieve, check, update here
		klog.Info("Found existing pod %s/%s in group %s", namespace, name, groupName)
		return nil
	}
	klog.Error("Did not find pod in table", group)

	// Prepare timestamp and podspec for insertion...
	ts := &pgtype.Timestamptz{Time: group.Timestamp.Time, Valid: true}
	podspec, err := json.Marshal(pod)
	if err != nil {
		return err
	}
	_, err = q.Pool.Query(ctx, insertPodQuery, string(podspec), pod.Namespace, pod.Name, ts, group.Name, group.Size)

	// Show the user a success or an error, we return it either way
	if err != nil {
		klog.Infof("Error inserting rows %s", err)
	}
	return err
}

// GetCreationTimestamp returns the creation time of a podGroup or a pod in seconds (time.MicroTime)
// We either get this from the pod itself (if size 1) or from the database
func (q *Queue) GetCreationTimestamp(pod *corev1.Pod, groupName string) (metav1.MicroTime, error) {

	// First see if we've seen the group before, the creation times are shared across a group
	ts := metav1.MicroTime{}

	// This query will fail if there are no rows (the podGroup is not known)
	err := q.Pool.QueryRow(context.Background(), getTimestampQuery, groupName).Scan(&ts)
	if err == nil {
		klog.Info("Creation timestamp is", ts)
		return ts, err
	}
	klog.Error("This is the error", err)

	// This is the first member of the group - use its CreationTimestamp
	if !pod.CreationTimestamp.IsZero() {
		return metav1.NewMicroTime(pod.CreationTimestamp.Time), nil
	}
	// If the pod for some reasond doesn't have a timestamp, assume now
	return metav1.NewMicroTime(time.Now()), nil
}
