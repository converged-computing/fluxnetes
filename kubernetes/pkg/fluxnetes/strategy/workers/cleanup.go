package workers

import (
	"context"
	"time"

	"google.golang.org/grpc"

	klog "k8s.io/klog/v2"

	pb "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc"

	"github.com/riverqueue/river"
)

type CleanupArgs struct {
	// We don't need to know this, but it's nice for the user to see
	GroupName string `json:"groupName"`
	FluxID    int64  `json:"fluxid"`
}

// The cleanup workers cleans up a reservation (issuing cancel)
func (args CleanupArgs) Kind() string { return "cleanup" }

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
}

// Work performs the AskFlux action. Cases include:
// Allocated: the job was successful and does not need to be re-queued. We return nil (completed)
// NotAllocated: the job cannot be allocated and needs to be requeued
// Not possible for some reason, likely needs a cancel
// Are there cases of scheduling out into the future further?
// See https://riverqueue.com/docs/snoozing-jobs
func (w CleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {
	klog.Infof("[CLEANUP-WORKER-START] Cleanup (cancel) running for jobid %s", job.Args.FluxID)

	// Connect to the Fluxion service. Returning an error means we retry
	// see: https://riverqueue.com/docs/job-retries
	conn, err := grpc.Dial("127.0.0.1:4242", grpc.WithInsecure())
	if err != nil {
		klog.Error("[Fluxnetes] AskFlux error connecting to server: %v\n", err)
		return err
	}
	defer conn.Close()

	//	Tell flux to cancel the job id
	fluxion := pb.NewFluxionServiceClient(conn)
	fluxionCtx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	// Prepare the request to cancel
	request := &pb.CancelRequest{
		FluxID: uint64(job.Args.FluxID),
	}

	// Assume if there is an error we should try again
	response, err := fluxion.Cancel(fluxionCtx, request)
	if err != nil {
		klog.Errorf("[Fluxnetes] Issue with cancel %s %s", response.Error, err)
		return err
	}

	// Collect rows into single result
	// pgx.CollectRows(rows, pgx.RowTo[string])
	// klog.Infof("Values: %s", values)
	klog.Infof("[CLEANUP-WORKER-COMPLETE] for group %s (flux job id %d)",
		job.Args.GroupName, job.Args.FluxID)
	return nil
}
