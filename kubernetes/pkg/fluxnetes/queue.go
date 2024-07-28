package fluxnetes

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klog "k8s.io/klog/v2"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	strategies "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy"
)

const (
	queueMaxWorkers = 10
)

// Queue holds handles to queue database and event handles
// The database Pool also allows interacting with the pods table (database.go)
type Queue struct {
	Pool          *pgxpool.Pool
	riverClient   *river.Client[pgx.Tx]
	EventChannels []*QueueEvent
	Strategy      strategies.QueueStrategy
}

type ChannelFunction func()

// QueueEvent holds the channel and defer function
type QueueEvent struct {
	Channel  <-chan *river.Event
	Function ChannelFunction
}

// NewQueue starts a new queue with a river client
func NewQueue(ctx context.Context) (*Queue, error) {
	dbPool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}

	// The default strategy now mirrors what fluence with Kubernetes does
	// This can eventually be customizable
	strategy := strategies.FCFSBackfill{}
	workers := river.NewWorkers()

	// Each strategy has its own worker type
	strategy.AddWorkers(workers)
	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Logger: slog.Default().With("id", "Fluxnetes"),
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: queueMaxWorkers},
		},
		Workers: workers,
	})
	if err != nil {
		return nil, err
	}

	// Create the queue and setup events for it
	err = riverClient.Start(ctx)
	if err != nil {
		return nil, err
	}
	queue := Queue{riverClient: riverClient, Pool: dbPool, Strategy: strategy}
	queue.setupEvents()
	return &queue, nil
}

// StopQueue creates a client (without calling start) only intended to
// issue stop, so we can leave out workers and queue from Config
func (q *Queue) Stop(ctx context.Context) error {
	if q.riverClient != nil {
		return q.riverClient.Stop(ctx)
	}
	return nil
}

// We can tell how a job runs via events
// setupEvents create subscription channels for each event type
func (q *Queue) setupEvents() {

	q.EventChannels = []*QueueEvent{}

	// Subscribers tell the River client the kinds of events they'd like to receive.
	// We add them to a listing to be used by Kubernetes. These can be subscribed
	// to from elsewhere too (anywhere)
	for _, event := range []river.EventKind{
		river.EventKindJobCompleted,
		river.EventKindJobCancelled,
		river.EventKindJobFailed,
		river.EventKindJobSnoozed,
	} {
		c, trigger := q.riverClient.Subscribe(event)
		channel := &QueueEvent{Function: trigger, Channel: c}
		q.EventChannels = append(q.EventChannels, channel)
	}
}

// Enqueue a new job to the provisional queue
// 1. Assemble (discover or define) the group
// 2. Add to provisional table
func (q *Queue) Enqueue(ctx context.Context, pod *corev1.Pod) error {

	// Get the pod name and size, first from labels, then defaults
	groupName := groups.GetPodGroupName(pod)
	size, err := groups.GetPodGroupSize(pod)
	if err != nil {
		return err
	}

	// Get the creation timestamp for the group
	ts, err := q.GetCreationTimestamp(pod, groupName)
	if err != nil {
		return err
	}

	// Log the namespace/name, group name, and size
	klog.Infof("Pod %s has Group %s (%d) created at %s", pod.Name, groupName, size, ts)

	// Add the pod to the provisional table.
	group := &groups.PodGroup{Size: size, Name: groupName, Timestamp: ts}
	return q.enqueueProvisional(ctx, pod, group)
}

// EnqueueProvisional adds a pod to the provisional queue
func (q *Queue) enqueueProvisional(ctx context.Context, pod *corev1.Pod, group *groups.PodGroup) error {

	// This query will fail if there are no rows (the podGroup is not known)
	var groupName, name, namespace string
	row := q.Pool.QueryRow(context.Background(), queries.GetPodQuery, group.Name, pod.Namespace, pod.Name)
	err := row.Scan(groupName, name, namespace)

	// We didn't find the pod in the table - add it.
	if err != nil {
		klog.Errorf("Did not find pod %s in group %s in table", pod.Name, group)

		// Prepare timestamp and podspec for insertion...
		ts := &pgtype.Timestamptz{Time: group.Timestamp.Time, Valid: true}
		podspec, err := json.Marshal(pod)
		if err != nil {
			return err
		}
		_, err = q.Pool.Query(ctx, queries.InsertPodQuery, string(podspec), pod.Namespace, pod.Name, ts, group.Name, group.Size)

		// Show the user a success or an error, we return it either way
		if err != nil {
			klog.Infof("Error inserting provisional pod %s", err)
		}
		return err
	}
	return err
}

// Schedule moves jobs from provisional to work queue
// This is based on a queue strategy. The default is fcfs with backfill.
// This mimics what Kubernetes does. Note that jobs can be sorted
// based on the scheduled at time AND priority.
func (q *Queue) Schedule(ctx context.Context) error {

	// Queue Strategy "Schedule" moves provional to the worker queue
	// We get them back in a back to schedule
	batch, err := q.Strategy.Schedule(ctx, q.Pool)
	if err != nil {
		return err
	}

	count, err := q.riverClient.InsertMany(ctx, batch)
	if err != nil {
		return err
	}
	klog.Infof("[Fluxnetes] Schedule inserted %d jobs\n", count)
	return nil
}

// GetCreationTimestamp returns the creation time of a podGroup or a pod in seconds (time.MicroTime)
// We either get this from the pod itself (if size 1) or from the database
func (q *Queue) GetCreationTimestamp(pod *corev1.Pod, groupName string) (metav1.MicroTime, error) {

	// First see if we've seen the group before, the creation times are shared across a group
	ts := metav1.MicroTime{}

	// This query will fail if there are no rows (the podGroup is not known)
	err := q.Pool.QueryRow(context.Background(), queries.GetTimestampQuery, groupName).Scan(&ts)
	if err == nil {
		klog.Info("Creation timestamp is", ts)
		return ts, err
	}

	// This is the first member of the group - use its CreationTimestamp
	if !pod.CreationTimestamp.IsZero() {
		return metav1.NewMicroTime(pod.CreationTimestamp.Time), nil
	}
	// If the pod for some reasond doesn't have a timestamp, assume now
	return metav1.NewMicroTime(time.Now()), nil
}
