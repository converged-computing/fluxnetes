package fluxnetes

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/logger"
)

const (
	queueMaxWorkers = 10
)

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

type JobArgs struct {
	ShouldSnooze bool `json:"shouldSnooze"`
	//	Jobspec      *pb.PodSpec
	GroupName string `json:"groupName"`
}

// The Kind MUST correspond to the <type>Args and <type>Worker
func (args JobArgs) Kind() string { return "job" }

type JobWorker struct {
	river.WorkerDefaults[JobArgs]
}

// Work performs the AskFlux action. Cases include:
// Allocated: the job was successful and does not need to be re-queued. We return nil (completed)
// NotAllocated: the job cannot be allocated and needs to be retried (Snoozed)
// Not possible for some reason, likely needs a cancel
// See https://riverqueue.com/docs/snoozing-jobs
func (w JobWorker) Work(ctx context.Context, job *river.Job[JobArgs]) error {
	l := logger.NewDebugLogger(logger.LevelDebug, "/tmp/workers.log")
	l.Info("I am running")
	klog.Infof("[WORKER]", "JobStatus", "Running")
	fmt.Println("I am running")
	if job.Args.ShouldSnooze {
		return river.JobSnooze(1 * time.Minute)
	}
	return nil
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

// Enqueue a new job to the queue
// When we add a job, we have generated the jobspec and the group is ready.
func (q *Queue) Enqueue(ctx context.Context, pod *corev1.Pod) error {
	// TODO create database that has pod names, etc.
	// We will add immediately to queue if no group
	// If there is a group, wait for minMember.
	// When we dispatch the group, will need to clean up this table
	groupName := "test-group"

	// TODO: this needs to be passed somehow to worker as json
	// serializable for reference later, for both pod/jobspec
	// Get the jobspec for the pod
	//jobspec := podspec.PreparePodJobSpec(pod, groupName)

	// Create an enqueue the new job!
	job := JobArgs{ShouldSnooze: true, GroupName: groupName}

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
	return err
}
