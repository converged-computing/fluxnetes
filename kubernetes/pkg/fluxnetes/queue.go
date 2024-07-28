package fluxnetes

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"

	//	v1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	// "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/podspec"
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
	workers := river.NewWorkers()
	river.AddWorker(workers, &JobWorker{})
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
	queue := Queue{riverClient: riverClient, Pool: dbPool}
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
	// We add them to a listing to be used by Kubernetes. Note that we are skipping
	// the snooze channel (schedule job for later)
	completedChan, completedFunc := q.riverClient.Subscribe(river.EventKindJobCompleted)
	channel := &QueueEvent{Function: completedFunc, Channel: completedChan}
	q.EventChannels = append(q.EventChannels, channel)

	failedChan, failedFunc := q.riverClient.Subscribe(river.EventKindJobFailed)
	channel = &QueueEvent{Function: failedFunc, Channel: failedChan}
	q.EventChannels = append(q.EventChannels, channel)

	delayChan, delayFunc := q.riverClient.Subscribe(river.EventKindJobSnoozed)
	channel = &QueueEvent{Function: delayFunc, Channel: delayChan}
	q.EventChannels = append(q.EventChannels, channel)

	cancelChan, cancelFunc := q.riverClient.Subscribe(river.EventKindJobCancelled)
	channel = &QueueEvent{Function: cancelFunc, Channel: cancelChan}
	q.EventChannels = append(q.EventChannels, channel)
}

// MoveQueue moves pods from provisional into the jobs queue
func (q *Queue) MoveQueue() {

	// If there is a group, wait for minMember.
	// When we dispatch the group, will need to clean up this table

}

// Enqueue a new job to the queue
// When we add a job, we have generated the jobspec and the group is ready.
func (q *Queue) Enqueue(ctx context.Context, pod *corev1.Pod) error {

	// Get the pod group - this does not alter the database, but just looks for it
	podGroup, err := q.GetPodGroup(pod)
	if err != nil {
		return err
	}
	klog.Infof("Derived podgroup %s (%d) created at %s", podGroup.Name, podGroup.Size, podGroup.Timestamp)

	// Case 1: add to queue if size 1 - we don't need to keep a record of it.
	if podGroup.Size == 1 {
		err = q.SubmitJob(ctx, pod, podGroup)
	}
	// Case 2: all other cases, add to pod table (if does not exist)
	// Pods added to the table are checked at the end (and Submit if
	// the group is ready, meaning having achieved minimum size.
	q.EnqueuePod(ctx, pod, podGroup)
	return nil
}

// SubmitJob subits the podgroup (and podspec) to the queue
// It will later be given to AskFlux
// TODO this should not rely on having a single pod.
func (q *Queue) SubmitJob(ctx context.Context, pod *corev1.Pod, group *PodGroup) error {

	// podspec needs to be serialized to pass to the job
	asJson, err := json.Marshal(pod)
	if err != nil {
		return err
	}

	// Create and submit the new job! This needs to serialize as json, hence converting
	// the podspec. It's not clear to me if we need to keep the original pod object
	job := JobArgs{GroupName: group.Name, Podspec: string(asJson), GroupSize: group.Size}

	// Start a transaction to insert a job - without this it won't run!
	tx, err := q.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	row, err := q.riverClient.InsertTx(ctx, tx, job, nil)
	err = tx.Commit(ctx)
	if err != nil {
		return err
	}
	if row.Job != nil {
		klog.Infof("Job %d was added to the queue", row.Job.ID)
	}
	return nil
}
