package strategy

import (
	"context"

	klog "k8s.io/klog/v2"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/defaults"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	work "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"
)

// FCFS with Backfill
// Schedule jobs that come in first, but allow smaller jobs to fill in
type FCFSBackfill struct{}

// Name returns shortened "first come first serve"
func (FCFSBackfill) Name() string {
	return "fcfs-backfill"
}

// Database Model we are retrieving for the Schedule function
// We will eventually want more than these three
type JobModel struct {
	GroupName string `db:"group_name"`
	GroupSize int32  `db:"group_size"`
	Podspec   string `db:"podspec"`
	// CreatedAt time.Time `db:"created_at"`
}

// GetWorker returns the worker for the queue strategy
// TODO(vsoch) rename this to be more specific for the worker type
func (FCFSBackfill) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &work.JobWorker{})
}

// queryReady checks if the number of pods we know of is >= required group size
// TODO(vsoch) This currently returns the entire table, and the sql needs to be tweaked
func (s FCFSBackfill) queryReady(ctx context.Context, pool *pgxpool.Pool) ([]work.JobArgs, error) {
	rows, err := pool.Query(ctx, queries.SelectGroupsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect rows into slice of jobs
	jobs := []work.JobArgs{}

	// Collect rows into single result
	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[JobModel])
	klog.Infof("Models: %s", models)

	// TODO(vsoch) we need to get unique podgroups here, and one representative podspec
	// we will eventually want to instead have the ability to support unique podspecs
	// under one group
	for _, model := range models {
		jobArgs := work.JobArgs{GroupName: model.GroupName, Podspec: model.Podspec, GroupSize: model.GroupSize}
		jobs = append(jobs, jobArgs)
	}
	klog.Infof("jobs: %s\n", jobs)
	return jobs, err
}

// Schedule moves pod groups from provisional to workers based on a strategy.
// We return a listing of river.JobArgs (JobArgs here) to be submit with batch.
// In this case it is first come first serve - we just sort based on the timestamp
// and add them to the worker queue. They run with they can, with smaller
// jobs being allowed to fill in. Other strategies will need to handle AskFlux
// and submitting batch differently.
func (s FCFSBackfill) Schedule(ctx context.Context, pool *pgxpool.Pool) ([]river.InsertManyParams, error) {

	// Is this group ready to be scheduled with the addition of this pod?
	jobs, err := s.queryReady(ctx, pool)
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
