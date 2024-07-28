package fluxnetes

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/grpc"

	//	v1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"

	pb "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/fluxion-grpc"

	"github.com/riverqueue/river"
	"k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/podspec"
)

type JobArgs struct {
	Jobspec   string `json:"jobspec"`
	Podspec   string `json:"podspec"`
	GroupName string `json:"groupName"`
	GroupSize int32  `json:"groupSize"`
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
	klog.Infof("[WORKER] JobStatus Running for group %s", job.Args.GroupName)

	// Convert jobspec back to json, and then pod
	var pod corev1.Pod
	err := json.Unmarshal([]byte(job.Args.Podspec), &pod)
	if err != nil {
		return err
	}

	// IMPORTANT: this is a JobSpec for *one* pod, assuming they are all the same.
	// This obviously may not be true if we have a hetereogenous PodGroup.
	// We name it based on the group, since it will represent the group
	jobspec := podspec.PreparePodJobSpec(&pod, job.Args.GroupName)
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
	_, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	// Prepare the request to allocate.
	request := &pb.MatchRequest{
		Ps:      jobspec,
		Request: "allocate",
		Count:   job.Args.GroupSize,
	}

	// An error here is an error with making the request
	response, err := fluxion.Match(context.Background(), request)
	if err != nil {
		klog.Error("[Fluxnetes] AskFlux did not receive any match response: %v\n", err)
		return err
	}
	klog.Info("Fluxion response %s", response)

	// TODO we need a way to pass this information back to the scheduler as a notification
	// TODO GetPodID should be renamed, because it will reflect the group
	// podGroupManager.log.Info("[PodGroup AskFlux] Match response ID %s\n", response.GetPodID())

	// Get the nodelist and inspect
	//	nodelist := response.GetNodelist()
	//	for _, node := range nodelist {
	//		nodes = append(nodes, node.NodeID)
	//	}
	//	jobid := uint64(response.GetJobID())
	//	podGroupManager.log.Info("[PodGroup AskFlux] parsed node pods list %s for job id %d\n", nodes, jobid)

	// TODO would be nice to actually be able to ask flux jobs -a to fluxnetes
	// That way we can verify assignments, etc.
	//	podGroupManager.mutex.Lock()
	//	podGroupManager.groupToJobId[groupName] = jobid
	//	podGroupManager.mutex.Unlock()
	//	return nodes, nil
	return nil
}
