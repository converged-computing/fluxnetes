package provisional

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"
	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"
)

// Job Database Model we are retrieving for jobs
// We will eventually want more than these three
type JobModel struct {
	GroupName string `db:"group_name"`
	GroupSize int32  `db:"group_size"`
	Podspec   string `db:"podspec"`
	// CreatedAt time.Time `db:"created_at"`
}

// The provisional queue is a custom queue (to go along with a queue strategy attached
// to a Fluxnetes.Queue) that handles ingesting single pods, and delivering them
// in a particular way (e.g., sorted by timestamp, by group, etc). Since these
// functions are shared between strategies, and called from Fluxnetes.Queue via
// the strategy, we organize here.
func NewProvisionalQueue(pool *pgxpool.Pool) *ProvisionalQueue {
	queue := ProvisionalQueue{pool: pool}
	return &queue
}

type ProvisionalQueue struct {
	pool *pgxpool.Pool
}

// Enqueue adds a pod to the provisional queue. A pool database connection is required,
// which comes from the main Fluxnetes queue.
func (q *ProvisionalQueue) Enqueue(
	ctx context.Context,
	pod *corev1.Pod,
	group *groups.PodGroup,
) error {

	// This query will fail if there are no rows (the podGroup is not known)
	var groupName, name, namespace string
	row := q.pool.QueryRow(context.Background(), queries.GetPodQuery, group.Name, pod.Namespace, pod.Name)
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
		_, err = q.pool.Query(ctx, queries.InsertPodQuery, string(podspec), pod.Namespace, pod.Name, ts, group.Name, group.Size)

		// Show the user a success or an error, we return it either way
		if err != nil {
			klog.Infof("Error inserting provisional pod %s", err)
		}
		return err
	}
	return err
}

// queryGroupsAtSize returns groups that have achieved minimum size
func (q *ProvisionalQueue) queryGroupsAtSize(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {

	// First retrieve the group names that are the right size
	rows, err := pool.Query(ctx, queries.SelectGroupsAtSizeQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect rows into single result
	groupNames, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return groupNames, err
}

// queryGroupsAtSize returns groups that have achieved minimum size
func (q *ProvisionalQueue) deleteGroups(ctx context.Context, pool *pgxpool.Pool, groupNames []string) error {

	// First retrieve the group names that are the right size
	query := fmt.Sprintf(queries.DeleteGroupsQuery, strings.Join(groupNames, ","))
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	return err
}

// queryGroupsAtSize returns groups that have achieved minimum size
func (q *ProvisionalQueue) getGroupsAtSize(ctx context.Context, pool *pgxpool.Pool, groupNames []string) ([]workers.JobArgs, error) {

	// Now we need to collect all the pods that match that.
	query := fmt.Sprintf(queries.SelectGroupsQuery, strings.Join(groupNames, "','"))
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Collect rows into map, and then slice of jobs
	// The map whittles down the groups into single entries
	// We will eventually not want to do that, assuming podspecs are different in a group
	jobs := []workers.JobArgs{}
	lookup := map[string]workers.JobArgs{}

	// Collect rows into single result
	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[JobModel])

	// TODO(vsoch) we need to collect all podspecs here and be able to give that to the worker
	for _, model := range models {
		jobArgs := workers.JobArgs{GroupName: model.GroupName, Podspec: model.Podspec, GroupSize: model.GroupSize}
		lookup[model.GroupName] = jobArgs
	}
	for _, jobArgs := range lookup {
		jobs = append(jobs, jobArgs)
	}
	return jobs, nil
}

// ReadyJobs returns jobs that are ready from the provisional table, also cleaning up
func (q *ProvisionalQueue) ReadyJobs(ctx context.Context, pool *pgxpool.Pool) ([]workers.JobArgs, error) {

	// 1. Get the list of group names that have pod count >= their size
	groupNames, err := q.queryGroupsAtSize(ctx, pool)
	if err != nil {
		return nil, err
	}

	// 2. Now we need to collect all the pods that match that.
	jobs, err := q.getGroupsAtSize(ctx, pool, groupNames)
	if err != nil {
		return nil, err
	}

	// 3. Finally, we need to delete them from the provisional table
	err = q.deleteGroups(ctx, pool, groupNames)
	return jobs, err
}
