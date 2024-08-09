package provisional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"
	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/strategy/workers"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/types"
)

// Job Database Model we are retrieving for jobs
// We will eventually want more than these three
type JobModel struct {
	GroupName string `db:"group_name"`
	Namespace string `db:"namespace"`
	GroupSize int32  `db:"group_size"`
	Duration  int32  `db:"duration"`
	Podspec   string `db:"podspec"`
}

// This collects the individual pod names and podspecs for the group
type PodModel struct {
	Name    string `db:"name"`
	Podspec string `db:"podspec"`
}

// GroupModel provides the group name and namespace for groups at size
type GroupModel struct {
	GroupName string `db:"group_name"`
	Namespace string `db:"namespace"`
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

// incrementGroupProvisional adds 1 to the count of the group provisional queue
func incrementGroupProvisional(
	ctx context.Context,
	pool *pgxpool.Pool,
	pod *corev1.Pod,
	group *groups.PodGroup,
) error {

	// Up the size of the group in provisional here
	query := fmt.Sprintf(queries.IncrementGroupProvisional, group.Name, pod.Namespace)
	klog.Infof("Incrementing group %s by 1 with pod %s", group.Name, pod.Name)
	_, err := pool.Exec(ctx, query)
	return err

}

// Enqueue adds a pod to the provisional queue, and if not yet added, the group to the group queue.
// provisional queue. A pool database connection is required,  which comes from the main Fluxnetes queue.
func (q *ProvisionalQueue) Enqueue(
	ctx context.Context,
	pod *corev1.Pod,
	group *groups.PodGroup,
) (types.EnqueueStatus, error) {

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		klog.Errorf("Issue creating new pool %s", err)
		return types.Unknown, err
	}
	defer pool.Close()

	// First check - a pod group in pending is not allowed to enqueue new pods.
	// This means the job is submit / running (and not completed
	result, err := pool.Exec(context.Background(), queries.IsPendingQuery, group.Name, pod.Namespace)
	if err != nil {
		klog.Infof("Error checking if pod %s/%s group is in pending queue", pod.Namespace, pod.Name)
		return types.Unknown, err
	}
	if strings.Contains(result.String(), "INSERT 1") {
		return types.GroupAlreadyInPending, nil
	}

	// Here we add to single pod provisional.
	// Prepare timestamp and podspec for insertion...
	podspec, err := json.Marshal(pod)
	if err != nil {
		klog.Infof("Error with pod marshall %s/%s when adding to provisional", pod.Namespace, pod.Name)
		return types.PodInvalid, err
	}

	// Insert or fall back if does not exists to doing nothing
	// TODO add back timestamp, and optimize this function to minimize database exec calls
	ts := &pgtype.Timestamptz{Time: group.Timestamp.Time, Valid: true}
	query := fmt.Sprintf(queries.InsertIntoProvisionalQuery, string(podspec), pod.Namespace, pod.Name, group.Duration, group.Name, group.Name, pod.Namespace, pod.Name)
	_, err = pool.Exec(context.Background(), query, ts)
	if err != nil {
		klog.Infof("Error inserting pod %s/%s into provisional queue", pod.Namespace, pod.Name)
		return types.Unknown, err
	}

	err = incrementGroupProvisional(context.Background(), pool, pod, group)
	if err != nil {
		klog.Infof("Error incrementing Pod %s/%s", pod.Namespace, pod.Name)
		return types.Unknown, err
	}

	// Next add to group provisional - will only add if does not exist
	// and if so, we make count 1 to avoid incremental call
	query = fmt.Sprintf(queries.InsertIntoGroupProvisional, group.Name, pod.Namespace, group.Size, group.Duration, string(podspec), group.Name, pod.Namespace)
	_, err = pool.Exec(ctx, query, ts)
	if err != nil {
		klog.Infof("Error inserting group into provisional %s", err)
		return types.Unknown, err
	}
	return types.PodEnqueueSuccess, nil
}

// getReadyGroups gets groups that are ready for moving from provisional to pending
// We also save the pod names so we can assign (bind) to nodes later
func (q *ProvisionalQueue) getReadyGroups(ctx context.Context, pool *pgxpool.Pool) ([]workers.JobArgs, error) {

	// First retrieve the group names that are the right size
	rows, err := pool.Query(ctx, queries.SelectGroupsAtSizeQuery)
	if err != nil {
		klog.Infof("GetReadGroups Error: select groups at size: %s", err)
		return nil, err
	}
	defer rows.Close()

	models, err := pgx.CollectRows(rows, pgx.RowToStructByName[JobModel])
	if err != nil {
		klog.Infof("GetReadGroups Error: collect rows for groups at size: %s", err)
		return nil, err
	}

	// Collect rows into map, and then slice of jobs
	// The map whittles down the groups into single entries
	// We will eventually not want to do that, assuming podspecs are different in a group
	jobs := []workers.JobArgs{}
	lookup := map[string]workers.JobArgs{}

	// Collect rows into single result
	// TODO(vsoch) we need to collect all podspecs here and be able to give that to the worker
	// Right now we just select a representative one for the entire group.
	for _, model := range models {

		podRows, err := q.pool.Query(ctx, queries.SelectPodsQuery, string(model.GroupName), string(model.Namespace))
		if err != nil {
			klog.Infof("SelectPodsQuery Error: query for pods for group %s: %s", model.GroupName, err)
			return nil, err
		}

		pods, err := pgx.CollectRows(podRows, pgx.RowToStructByName[PodModel])
		if err != nil {
			klog.Infof("SelectPodsQuery Error: collect rows for groups %s: %s", model.GroupName, err)
			return nil, err
		}

		// Assemble one podspec, and list of pods that we will need
		podlist := []string{}
		var podspec string
		for _, pod := range pods {
			podspec = pod.Podspec
			podlist = append(podlist, pod.Name)
		}
		klog.Infof("parsing group %s", model)
		jobArgs := workers.JobArgs{
			GroupName: model.GroupName,
			GroupSize: model.GroupSize,
			Duration:  model.Duration,
			Podspec:   podspec,
			Namespace: model.Namespace,
			Names:     strings.Join(podlist, ","),
		}
		lookup[model.GroupName+"-"+model.Namespace] = jobArgs
	}
	for _, jobArgs := range lookup {
		jobs = append(jobs, jobArgs)
	}
	return jobs, nil
}

// deleteGroups deletes groups from the provisional table
func (q *ProvisionalQueue) deleteGroups(
	ctx context.Context,
	pool *pgxpool.Pool,
	groups []workers.JobArgs,
) error {

	// select based on group name and namespace, which should be unique
	query := ""
	for i, group := range groups {
		query += fmt.Sprintf("(group_name = '%s' and namespace='%s')", group.GroupName, group.Namespace)
		if i < len(groups)-1 {
			query += " or "
		}
	}
	klog.Infof("Query is %s", query)

	// Note that we don't delete from the single pod provisional table
	// until we have used it to get the podspec (and job is complete)

	// Delete from the group provisional table, which we don't need anymore
	query = fmt.Sprintf(queries.DeleteProvisionalGroupsQuery, query)
	_, err := pool.Exec(ctx, query)
	if err != nil {
		klog.Infof("Error with delete groups provisional %s: %s", query, err)
		return err
	}
	return err
}

// Enqueue adds a pod to the provisional queue. A pool database connection is required,
// which comes from the main Fluxnetes queue.
func (q *ProvisionalQueue) insertPending(
	ctx context.Context,
	pool *pgxpool.Pool,
	groups []workers.JobArgs,
) error {

	// Send in patch
	batch := &pgx.Batch{}
	for _, group := range groups {
		query := fmt.Sprintf(queries.InsertIntoPending, group.GroupName, group.Namespace, group.GroupSize, group.GroupName, group.Namespace)
		batch.Queue(query)
	}
	klog.Infof("[Fluxnetes] Inserting %d groups into pending\n", len(groups))
	result := pool.SendBatch(ctx, batch)
	err := result.Close()
	if err != nil {
		klog.Errorf("Error committing to send %d groups into pending %s", len(groups), err)
	}
	return err
}

// ReadyJobs returns jobs that are ready from the provisional table, also cleaning up
func (q *ProvisionalQueue) ReadyJobs(ctx context.Context, pool *pgxpool.Pool) ([]workers.JobArgs, error) {

	// 1. Get the list of group names that have pod count >= their size
	jobs, err := q.getReadyGroups(ctx, pool)
	if err != nil {
		return nil, err
	}

	klog.Infof("Found %d ready groups %s", len(jobs), jobs)
	if len(jobs) > 0 {

		// Move them into pending! We do this first so that we are sure the groups
		// are known to be pending before we delete from provisional.
		err = q.insertPending(ctx, pool, jobs)
		if err != nil {
			return nil, err
		}
		// 3. Finally, we need to delete them from the provisional tables
		// If more individual pods are added, they need to be a new group
		err = q.deleteGroups(ctx, pool, jobs)
	}
	return jobs, err
}
