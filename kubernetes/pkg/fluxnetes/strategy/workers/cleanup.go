package workers

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/grpc"

	klog "k8s.io/klog/v2"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/defaults"
	pb "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc"

	"github.com/riverqueue/river"
)

type CleanupArgs struct {
	// We don't need to know this, but it's nice for the user to see
	GroupName string `json:"groupName"`
	FluxID    int64  `json:"fluxid"`

	// Do we need to cleanup Kubernetes too?
	Kubernetes bool `json:"kubernetes"`
}

// The cleanup workers cleans up a reservation (issuing cancel)
func (args CleanupArgs) Kind() string { return "cleanup" }

type CleanupWorker struct {
	river.WorkerDefaults[CleanupArgs]
}

// SubmitCleanup submits a cleanup job N seconds into the future
func SubmitCleanup(
	ctx context.Context,
	pool *pgxpool.Pool,
	seconds int32,
	fluxID int64,
	inKubernetes bool,
	tags []string,
) error {

	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return fmt.Errorf("error getting client from context: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Create scheduledAt time - N seconds from now
	now := time.Now()
	scheduledAt := now.Add(time.Second * time.Duration(seconds))

	insertOpts := river.InsertOpts{
		MaxAttempts: defaults.MaxAttempts,
		Tags:        tags,
		Queue:       "cleanup_queue",
		ScheduledAt: scheduledAt,
	}
	_, err = client.InsertTx(ctx, tx, CleanupArgs{FluxID: fluxID, Kubernetes: inKubernetes}, insertOpts)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

// Work performs the Cancel action
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
