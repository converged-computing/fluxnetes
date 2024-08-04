package fluxion

import (
	"os"

	"github.com/converged-computing/fluxnetes/pkg/defaults"
	pb "github.com/converged-computing/fluxnetes/pkg/fluxion-grpc"
	"github.com/converged-computing/fluxnetes/pkg/jobspec"
	utils "github.com/converged-computing/fluxnetes/pkg/utils"
	"github.com/flux-framework/fluxion-go/pkg/fluxcli"
	klog "k8s.io/klog/v2"

	"context"
	"errors"
)

type Fluxion struct {
	cli *fluxcli.ReapiClient
	pb.UnimplementedFluxionServiceServer
}

// InitFluxion creates a new client to interaction with the fluxion API (via go bindings)
func (fluxion *Fluxion) InitFluxion(policy, label string) {
	fluxion.cli = fluxcli.NewReapiClient()

	klog.Infof("[Fluxnetes] Created flux resource client %s", fluxion.cli)
	err := utils.CreateInClusterJGF(defaults.KubernetesJsonGraphFormat, label)
	if err != nil {
		return
	}

	// This file needs to be written for GetResources to read later
	jgf, err := os.ReadFile(defaults.KubernetesJsonGraphFormat)
	if err != nil {
		klog.Error("Error reading JGF")
		return
	}

	p := "{}"
	if policy != "" {
		p = string("{\"matcher_policy\": \"" + policy + "\"}")
		klog.Infof("[Fluxnetes] match policy: %s", p)
	}
	fluxion.cli.InitContext(string(jgf), p)
}

// Cancel wraps the Cancel function of the fluxion go bindings
func (fluxion *Fluxion) Cancel(
	ctx context.Context,
	in *pb.CancelRequest,
) (*pb.CancelResponse, error) {

	klog.Infof("[Fluxnetes] received cancel request %v\n", in)
	err := fluxion.cli.Cancel(int64(in.FluxID), in.NoExistOK)
	if err != nil {
		return nil, err
	}

	// Why would we have an error code here if we check above?
	// This (I think) should be an error code for the specific job
	dr := &pb.CancelResponse{FluxID: in.FluxID}
	klog.Infof("[Fluxnetes] sending cancel response %v\n", dr)
	klog.Infof("[Fluxnetes] cancel errors so far: %s\n", fluxion.cli.GetErrMsg())

	reserved, at, overhead, mode, fluxerr := fluxion.cli.Info(int64(in.FluxID))
	klog.Infof("\n\t----Job Info output---")
	klog.Infof("jobid: %d\nreserved: %t\nat: %d\noverhead: %f\nmode: %s\nerror: %d\n", in.FluxID, reserved, at, overhead, mode, fluxerr)

	klog.Infof("[GRPCServer] Sending Cancel response %v\n", dr)
	return dr, nil
}

// Match wraps the MatchAllocate function of the fluxion go bindings
// If a match is not possible, we return an empty response with allocated false
// This should only return an error if there is some issue with Fluxion
// or the task of matching.
func (fluxion *Fluxion) Match(ctx context.Context, in *pb.MatchRequest) (*pb.MatchResponse, error) {

	emptyResponse := &pb.MatchResponse{}

	// Prepare an empty match response (that can still be serialized)
	klog.Infof("[Fluxnetes] Received Match request %v\n", in)

	// Generate the jobspec, array of bytes converted to string
	spec, err := jobspec.CreateJobSpecYaml(in.Podspec, in.Count)
	if err != nil {
		return emptyResponse, err
	}

	// Ask flux to match allocate, either with or without a reservation
	reserved, allocated, at, overhead, jobid, fluxerr := fluxion.cli.MatchAllocate(in.Reserve, string(spec))
	utils.PrintOutput(reserved, allocated, at, overhead, jobid, fluxerr)

	// Be explicit about errors (or not)
	// These errors are related to matching, not whether it is possible or not,
	// and should not happen.
	errorMessages := fluxion.cli.GetErrMsg()
	if errorMessages == "" {
		klog.Info("[Fluxnetes] There are no errors")
	} else {
		klog.Infof("[Fluxnetes] Match errors so far %s", errorMessages)
	}
	if fluxerr != nil {
		klog.Errorf("[Fluxnetes] Match Flux err is", fluxerr)
		return emptyResponse, errors.New("[Fluxnetes] Error in ReapiCliMatchAllocate")
	}

	// It's OK if we can't allocate, it means we can reserve and let the client
	// handle this information how they see fit. If we can allocate, we return
	// the nodes.
	nodelist := []*pb.NodeAlloc{}
	haveAllocation := allocated != ""

	if haveAllocation {
		// Pass the job name (the group) for inspection/ordering later
		nodetasks := utils.ParseAllocResult(allocated, in.JobName)
		nodelist = make([]*pb.NodeAlloc, len(nodetasks))
		for i, result := range nodetasks {
			nodelist[i] = &pb.NodeAlloc{
				NodeID: result.Basename,
				Tasks:  int32(result.CoreCount) / in.Podspec.Cpu,
			}
		}
	}

	mr := &pb.MatchResponse{
		Nodelist:   nodelist,
		FluxID:     uint64(jobid),
		Reserved:   reserved,
		ReservedAt: at,
		Allocated:  haveAllocation,
	}
	klog.Infof("[Fluxnetes] Match response %v \n", mr)
	return mr, nil
}
