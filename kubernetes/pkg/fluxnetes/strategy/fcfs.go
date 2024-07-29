package strategy

import (
	"context"
	"fmt"
	"strings"

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

// queryGroupsAtSize returns groups that have achieved minimum size
func (s FCFSBackfill) queryGroupsAtSize(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {

	// First retrieve the group names that are the right size
	rows, err := pool.Query(ctx, queries.SelectGroupsAtSizeQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect rows into single result
	groupNames, err := pgx.CollectRows(rows, pgx.RowTo[string])
	klog.Infof("GROUP NAMES %s", groupNames)
	return groupNames, err
}

// queryGroupsAtSize returns groups that have achieved minimum size
func (s FCFSBackfill) deleteGroups(ctx context.Context, pool *pgxpool.Pool, groupNames []string) error {

	// First retrieve the group names that are the right size
	query := fmt.Sprintf(queries.DeleteGroupsQuery, strings.Join(groupNames, ","))
	klog.Infof("DELETE %s", query)
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	return err
}

// queryGroupsAtSize returns groups that have achieved minimum size
func (s FCFSBackfill) getGroupsAtSize(ctx context.Context, pool *pgxpool.Pool, groupNames []string) ([]work.JobArgs, error) {

	// Now we need to collect all the pods that match that.
	query := fmt.Sprintf(queries.SelectGroupsQuery, strings.Join(groupNames, "','"))
	klog.Infof("GET %s", query)
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect rows into map, and then slice of jobs
	// The map whittles down the groups into single entries
	// We will eventually not want to do that, assuming podspecs are different in a group
	jobs := []work.JobArgs{}
	lookup := map[string]work.JobArgs{}

	// Collect rows into single result
	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[JobModel])

	// TODO(vsoch) we need to collect all podspecs here and be able to give that to the worker
	for _, model := range models {
		jobArgs := work.JobArgs{GroupName: model.GroupName, Podspec: model.Podspec, GroupSize: model.GroupSize}
		lookup[model.GroupName] = jobArgs
	}
	for _, jobArgs := range lookup {
		jobs = append(jobs, jobArgs)
	}
	return jobs, nil
}

// queryReady checks if the number of pods we know of is >= required group size
// TODO(vsoch) This currently returns the entire table, and the sql needs to be tweaked
func (s FCFSBackfill) queryReady(ctx context.Context, pool *pgxpool.Pool) ([]work.JobArgs, error) {

	// 1. Get the list of group names that have pod count >= their size
	groupNames, err := s.queryGroupsAtSize(ctx, pool)
	if err != nil {
		return nil, err
	}

	// 2. Now we need to collect all the pods that match that.
	jobs, err := s.getGroupsAtSize(ctx, pool, groupNames)
	if err != nil {
		return nil, err
	}

	// 3. Finally, we need to delete them from provisional
	err = s.deleteGroups(ctx, pool, groupNames)
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
