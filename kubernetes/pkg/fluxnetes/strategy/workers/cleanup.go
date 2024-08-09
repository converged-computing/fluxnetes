package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	klog "k8s.io/klog/v2"

	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/defaults"
	pb "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/queries"

	"github.com/riverqueue/river"
)

type CleanupArgs struct {
	// We don't need to know this, but it's nice for the user to see
	GroupName string `json:"groupName"`
	FluxID    int64  `json:"fluxid"`
	Podspec   string `json:"podspec"`

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
	seconds *int64,
	podspec string,
	fluxID int64,
	inKubernetes bool,
	tags []string,
) error {

	klog.Infof("SUBMIT CLEANUP starting for %d", fluxID)

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
	scheduledAt := now.Add(time.Second * time.Duration(*seconds))

	insertOpts := river.InsertOpts{
		MaxAttempts: defaults.MaxAttempts,
		Tags:        tags,
		Queue:       "cancel_queue",
		ScheduledAt: scheduledAt,
	}
	_, err = client.InsertTx(ctx, tx, CleanupArgs{FluxID: fluxID, Kubernetes: inKubernetes, Podspec: podspec}, &insertOpts)
	if err != nil {
		return err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return err
	}
	if fluxID < 0 {
		klog.Infof("SUBMIT CLEANUP ending for unschedulable job")
	} else {
		klog.Infof("SUBMIT CLEANUP ending for %d", fluxID)
	}
	return nil
}

// deleteObjects cleans up (deletes) Kubernetes objects
// We do this before the call to fluxion so we can be sure the
// cluster object resources are freed first
func deleteObjects(ctx context.Context, podspec string) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	// Serialize the podspec back to a pod
	var pod corev1.Pod
	err = json.Unmarshal([]byte(podspec), &pod)
	if err != nil {
		return err
	}

	// If we only have the pod (no owner references) we can just delete it.
	if len(pod.ObjectMeta.OwnerReferences) == 0 {
		klog.Infof("Single pod cleanup for %s/%s", pod.Namespace, pod.Name)
		deletePolicy := metav1.DeletePropagationForeground
		opts := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}
		return clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, opts)
	}

	// If we get here, we are deleting an owner. It can (for now) be: job
	// We can add other types as they come in!
	for _, owner := range pod.ObjectMeta.OwnerReferences {
		klog.Infof("Pod %s/%s has owner %s with UID %s", pod.Namespace, pod.Name, owner.Kind, owner.UID)
		if owner.Kind == "Job" {
			return deleteJob(ctx, pod.Namespace, clientset, owner)
		}
		// Important: need to figure out what to do with BlockOwnerDeletion
		// https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apimachinery/pkg/apis/meta/v1/types.go#L319
	}
	return nil
}

// deleteJob handles deletion of a Job
func deleteJob(ctx context.Context, namespace string, client kubernetes.Interface, owner metav1.OwnerReference) error {
	job, err := client.BatchV1().Jobs(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	klog.Infof("Found job %s/%s", job.Namespace, job.Name)

	// This needs to be background for pods
	deletePolicy := metav1.DeletePropagationBackground
	opts := metav1.DeleteOptions{PropagationPolicy: &deletePolicy}
	return client.BatchV1().Jobs(namespace).Delete(ctx, job.Name, opts)
}

// Work performs the Cancel action, first cancelling in Kubernetes (if needed)
// and then cancelling in fluxion.
func (w CleanupWorker) Work(ctx context.Context, job *river.Job[CleanupArgs]) error {

	// Wrapper to actual cleanup function that can be called from elsewhere
	return Cleanup(ctx, job.Args.Podspec, job.Args.FluxID, job.Args.Kubernetes, job.Args.GroupName)
}

// Cleanup handles a call to fluxion to cancel (if appropriate) along with Kubernetes object deletion,
// and finally, deletion from Pending queue (table) to allow new jobs in
func Cleanup(
	ctx context.Context,
	podspec string,
	fluxID int64,
	inKubernetes bool,
	groupName string,
) error {

	klog.Infof("[CLEANUP-START] Cleanup (cancel) running for jobid %d", fluxID)

	// First attempt cleanup in the cluster, only if in Kubernetes
	if inKubernetes {
		err := deleteObjects(ctx, podspec)

		// The job might have been deleted another way
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	// We only delete from fluxion if there is a flux id
	// A valid fluxID is 0 or greater
	var err error
	if fluxID > -1 {
		err = deleteFluxion(fluxID)
		if err != nil {
			klog.Infof("Error issuing cancel to fluxion for group '%s' and fluxID %d", groupName, fluxID)
		}
		return err
	}

	// Serialize the podspec back to a pod
	var pod corev1.Pod
	err = json.Unmarshal([]byte(podspec), &pod)
	if err != nil {
		return err
	}

	// Next, delete from the pending table to new pods with same group
	// TODO should we allow this to continue?
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		klog.Errorf("Issue creating new pool during cancel: %s", err)
		return err
	}
	defer pool.Close()

	// Delete from pending and pods provisional, meaning we are allowed to accept new pods for the group
	_, err = pool.Exec(context.Background(), queries.DeleteProvisionalGroupsQuery, groupName, pod.Namespace)
	if err != nil {
		klog.Infof("Error deleting Pods %s/%s from provisional queue", pod.Namespace, pod.Name)
		return err
	}

	_, err = pool.Exec(context.Background(), queries.DeleteFromPendingQuery, groupName, pod.Namespace)
	if err != nil {
		klog.Infof("Error deleting Pod %s/%s from pending queue", pod.Namespace, pod.Name)
		return err
	}
	klog.Infof("[CLEANUP-COMPLETE] for group %s (flux job id %d)", groupName, fluxID)
	return nil
}

// deleteFluxion issues a cancel to Fluxion, our scheduler
func deleteFluxion(fluxID int64) error {

	// Connect to the Fluxion service. Returning an error means we retry
	// see: https://riverqueue.com/docs/job-retries
	conn, err := grpc.Dial("127.0.0.1:4242", grpc.WithInsecure())
	if err != nil {
		return fmt.Errorf("[Fluxnetes] AskFlux error connecting to server: %v\n", err)
	}
	defer conn.Close()

	//	Tell flux to cancel the job id
	fluxion := pb.NewFluxionServiceClient(conn)
	fluxionCtx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	// Prepare the request to cancel
	// https://github.com/flux-framework/flux-sched/blob/master/resource/reapi/bindings/go/src/fluxcli/reapi_cli.go#L226
	request := &pb.CancelRequest{
		FluxID: uint64(fluxID),

		// Don't return an error if the job id does not exist. See:
		NoExistOK: true,
	}

	// Assume if there is an error we should try again
	// TODO:(vsoch) How to distinguish between cancel error
	// and possible already cancelled?
	_, err = fluxion.Cancel(fluxionCtx, request)
	if err != nil {
		return fmt.Errorf("[Fluxnetes] Issue with cancel %s", err)
	}
	klog.Infof("[Fluxnetes] Successful cancel for jobid %d", fluxID)
	return err
}
