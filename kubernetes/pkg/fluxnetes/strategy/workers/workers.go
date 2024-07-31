package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	pb "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc"

	"github.com/riverqueue/river"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/resources"
)

type JobArgs struct {

	// Submit Args
	Jobspec   string `json:"jobspec"`
	Podspec   string `json:"podspec"`
	GroupName string `json:"groupName"`
	GroupSize int32  `json:"groupSize"`

	// Nodes return to Kubernetes to bind, and MUST
	// have attributes for the Nodes and Podspecs.
	// We can eventually have a kubectl command
	// to get a job too ;)
	Nodes   string `json:"nodes"`
	FluxJob int64  `json:"jobid"`
	PodId   string `json:"podid"`
}

// The Kind MUST correspond to the <type>Args and <type>Worker
func (args JobArgs) Kind() string { return "job" }

type JobWorker struct {
	river.WorkerDefaults[JobArgs]
}

// Work performs the AskFlux action. Cases include:
// Allocated: the job was successful and does not need to be re-queued. We return nil (completed)
// NotAllocated: the job cannot be allocated and needs to be requeued
// Not possible for some reason, likely needs a cancel
// Are there cases of scheduling out into the future further?
// See https://riverqueue.com/docs/snoozing-jobs
func (w JobWorker) Work(ctx context.Context, job *river.Job[JobArgs]) error {
	klog.Infof("[WORKER-START] JobStatus Running for group %s", job.Args.GroupName)

	// Convert jobspec back to json, and then pod
	var pod corev1.Pod
	err := json.Unmarshal([]byte(job.Args.Podspec), &pod)
	if err != nil {
		return err
	}

	// IMPORTANT: this is a JobSpec for *one* pod, assuming they are all the same.
	// This obviously may not be true if we have a hetereogenous PodGroup.
	// We name it based on the group, since it will represent the group
	jobspec := resources.PreparePodJobSpec(&pod, job.Args.GroupName)
	klog.Infof("Prepared pod jobspec %s", jobspec)

	// Connect to the Fluxion service. Returning an error means we retry
	// see: https://riverqueue.com/docs/job-retries
	conn, err := grpc.Dial("127.0.0.1:4242", grpc.WithInsecure())
	if err != nil {
		klog.Error("[Fluxnetes] AskFlux error connecting to server: %v\n", err)
		return err
	}
	defer conn.Close()

	//	Let's ask Flux if we can allocate the job!
	fluxion := pb.NewFluxionServiceClient(conn)
	fluxionCtx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	// Prepare the request to allocate.
	// Note that reserve will just give an ETA for the future.
	// We don't want to actually run this job again then, because newer
	// jobs could come in and take precendence. It's more an FYI for the
	// user when we expose some kubectl tool.
	request := &pb.MatchRequest{
		Podspec: jobspec,
		Reserve: true,
		Count:   job.Args.GroupSize,
		JobName: job.Args.GroupName,
	}

	// An error here is an error with making the request, nothing about
	// the match/allocation itself.
	response, err := fluxion.Match(fluxionCtx, request)
	if err != nil {
		klog.Error("[Fluxnetes] AskFlux did not receive any match response", err)
		return err
	}

	// This means we didn't get an allocation - we might have a reservation (to do
	// something with later) but for now we just print it.
	if !response.Allocated {
		errorMessage := fmt.Sprintf("Fluxion could not allocate nodes for %s, ETA %d", job.Args.GroupName, response.ReservedAt)
		klog.Info(errorMessage)

		// This will have the job be retried in the queue, still based on sorted schedule time and priority
		return fmt.Errorf(errorMessage)
	}
	klog.Infof("Fluxion response with allocation is %s", response)

	// These don't actually update, eventually we can update them also in the database update
	// We update the "Args" of the job to pass the node assignment back to the scheduler
	// job.Args.PodId = response.GetPodID()
	// job.Args.FluxJob = response.GetJobID()

	// Get the nodelist and serialize into list of strings for job args
	nodelist := response.GetNodelist()
	nodes := []string{}
	for _, node := range nodelist {
		nodes = append(nodes, node.NodeID)
	}
	nodeStr := strings.Join(nodes, ",")

	// Convert the response into an error code that indicates if we should run again.
	// We must update the database with nodes from here with a query
	// This will be sent back to the Kubernetes scheduler
	// TODO(vsoch): should this be error (which will retry) or cancel (not)?
	pool, err := pgxpool.New(fluxionCtx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	rows, err := pool.Query(fluxionCtx, queries.UpdateNodesQuery, nodeStr, job.ID)
	if err != nil {
		return err
	}
	defer rows.Close()

	// TODO why aren't events being all sent sometimes?
	// try putting subscription elsewhere?

	// Collect rows into single result
	// pgx.CollectRows(rows, pgx.RowTo[string])
	// klog.Infof("Values: %s", values)
	klog.Infof("[WORKER-COMPLETE] nodes allocated %s for group %s (flux job id %d)\n",
		nodeStr, job.Args.GroupName, job.Args.FluxJob)
	return nil
}

// If needed, to get a client from a worker (to submit more jobs)
// client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
// if err != nil {
//	return fmt.Errorf("error getting client from context: %w", err)
// }
