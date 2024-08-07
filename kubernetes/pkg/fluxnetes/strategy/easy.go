package strategy

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/defaults"
	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/provisional"
	work "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/types"
)

// Easy with Backfill
// Schedule jobs that come in first, but allow smaller jobs to fill in
type EasyBackfill struct{}

// Name returns shortened "first come first serve"
func (EasyBackfill) Name() string {
	return "easy"
}

// GetReservationDepth returns the depth of 1
// easy allows for one reservation.
func (EasyBackfill) GetReservationDepth() int32 {
	return int32(1)
}

// ReservationModel makes it easy to convert a rows response
// into the data structures here
type ReservationModel struct {
	GroupName string `db:"group_name"`
	FluxID    int64  `db:"flux_id"`
}

// AddtWorkers adds the worker for the queue strategy
// job worker: a queue to submit jobs to fluxion
// cleanup worker: a queue to cleanup
func (EasyBackfill) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &work.JobWorker{})
	river.AddWorker(workers, &work.CleanupWorker{})
}

// Schedule moves pod groups from provisional to workers based on a strategy.
// We return a listing of river.JobArgs (JobArgs here) to be submit with batch.
// In this case it is first come first serve - we just sort based on the timestamp
// and add them to the worker queue. They run with they can, with smaller
// jobs being allowed to fill in. Other strategies will need to handle AskFlux
// and submitting batch differently.
func (s EasyBackfill) Schedule(
	ctx context.Context,
	pool *pgxpool.Pool,
	reservationDepth int32,
) ([]river.InsertManyParams, error) {

	pending := provisional.NewProvisionalQueue(pool)

	// Is this group ready to be scheduled with the addition of this pod?
	jobs, err := pending.ReadyJobs(ctx, pool)
	if err != nil {
		klog.Errorf("Issue FCFS with backfill querying for ready groups", err)
		return nil, err
	}

	// Shared insertOpts.
	// Tags can eventually be specific to job attributes, queues, etc.
	// The default queue is used for allocation requests
	insertOpts := river.InsertOpts{
		MaxAttempts: defaults.MaxAttempts,
		Tags:        []string{s.Name()},
		Queue:       river.QueueDefault,
	}

	// https://riverqueue.com/docs/batch-job-insertion
	// Note: this is how to eventually add Priority (1-4, 4 is lowest)
	// And we can customize other InsertOpts. Of interest is Pending:
	// https://github.com/riverqueue/river/blob/master/insert_opts.go#L35-L40
	// Note also that ScheduledAt can be used for a reservation!
	batch := []river.InsertManyParams{}
	for i, jobArgs := range jobs {
		args := river.InsertManyParams{Args: jobArgs, InsertOpts: &insertOpts}
		if int32(i) < reservationDepth {
			jobArgs.Reservation = true
		}
		batch = append(batch, args)
	}
	return batch, nil
}

// PostSubmit does clearing of reservations
// unlike schedule, we provide the client here to do the work, the reason being
// we might want to check success and do an additional operation (delete).
// This happens after we enqueue a new set, assuming the cycle of workers has also
// moved through the queue. In practice (with async) this might not be true. We
// need to draw a state diagram to figure this out.
func (s EasyBackfill) PostSubmit(
	ctx context.Context,
	pool *pgxpool.Pool,
	riverClient *river.Client[pgx.Tx],
) error {

	// Shared insertOpts.
	// Tags can eventually be specific to job attributes, queues, etc.
	// This also sets the queue to the cleanup queue
	insertOpts := river.InsertOpts{
		MaxAttempts: defaults.MaxAttempts,
		Tags:        []string{s.Name()},
		Queue:       "cleanup_queue",
	}

	// Get list of flux ids to cancel
	// Now we need to collect all the pods that match that.
	rows, err := pool.Query(ctx, queries.GetReservationsQuery)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Collect rows into map, and then slice of cleanup args
	// A cleanup worker issues a cancel request to fluxion
	reservations := []work.CleanupArgs{}

	// Collect rows into single result
	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[ReservationModel])
	for _, model := range models {
		cleanupArgs := work.CleanupArgs{GroupName: model.GroupName, FluxID: model.FluxID}
		reservations = append(reservations, cleanupArgs)
	}

	// Prepare batch job for cleanup workers
	batch := []river.InsertManyParams{}
	for _, cleanupArgs := range reservations {
		args := river.InsertManyParams{Args: cleanupArgs, InsertOpts: &insertOpts}
		batch = append(batch, args)
	}

	// Insert the cleanup jobs
	if len(batch) > 0 {
		count, err := riverClient.InsertMany(ctx, batch)
		if err != nil {
			return err
		}
		klog.Infof("[easy] post cleanup (cancel) of %d jobs", count)

		// Now cleanup!
		dRows, err := pool.Query(ctx, queries.DeleteReservationsQuery)
		if err != nil {
			return err
		}
		defer dRows.Close()
	}
	return nil
}

func (s EasyBackfill) Enqueue(
	ctx context.Context,
	pool *pgxpool.Pool,
	pod *corev1.Pod,
	group *groups.PodGroup,
) (types.EnqueueStatus, error) {
	pending := provisional.NewProvisionalQueue(pool)
	return pending.Enqueue(ctx, pod, group)
}
