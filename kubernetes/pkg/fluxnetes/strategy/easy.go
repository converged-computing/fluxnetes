package strategy

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/defaults"
	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/provisional"
	work "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"
)

// Easy with Backfill
// Schedule jobs that come in first, but allow smaller jobs to fill in
type EasyBackfill struct{}

// Name returns shortened "first come first serve"
func (EasyBackfill) Name() string {
	return "easy"
}

// GetWorker returns the worker for the queue strategy
// TODO(vsoch) rename this to be more specific for the worker type
func (EasyBackfill) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &work.JobWorker{})
}

// Schedule moves pod groups from provisional to workers based on a strategy.
// We return a listing of river.JobArgs (JobArgs here) to be submit with batch.
// In this case it is first come first serve - we just sort based on the timestamp
// and add them to the worker queue. They run with they can, with smaller
// jobs being allowed to fill in. Other strategies will need to handle AskFlux
// and submitting batch differently.
func (s EasyBackfill) Schedule(ctx context.Context, pool *pgxpool.Pool) ([]river.InsertManyParams, error) {

	// TODO move logic from here into provisional
	pending := provisional.NewProvisionalQueue(pool)

	// Is this group ready to be scheduled with the addition of this pod?
	jobs, err := pending.ReadyJobs(ctx, pool)
	if err != nil {
		klog.Errorf("Issue FCFS with backfill querying for ready groups", err)
		return nil, err
	}

	// Shared insertOpts.
	// Tags can eventually be specific to job attributes, queues, etc.
	insertOpts := river.InsertOpts{
		MaxAttempts: defaults.MaxAttempts,
		Tags:        []string{s.Name()},
	}

	// https://riverqueue.com/docs/batch-job-insertion
	// Note: this is how to eventually add Priority (1-4, 4 is lowest)
	// And we can customize other InsertOpts. Of interest is Pending:
	// https://github.com/riverqueue/river/blob/master/insert_opts.go#L35-L40
	// Note also that ScheduledAt can be used for a reservation!
	batch := []river.InsertManyParams{}
	for _, jobArgs := range jobs {
		args := river.InsertManyParams{Args: jobArgs, InsertOpts: &insertOpts}
		batch = append(batch, args)
	}
	return batch, err
}

func (s EasyBackfill) Enqueue(
	ctx context.Context,
	pool *pgxpool.Pool,
	pod *corev1.Pod,
	group *groups.PodGroup,
) error {
	pending := provisional.NewProvisionalQueue(pool)
	return pending.Enqueue(ctx, pod, group)
}
