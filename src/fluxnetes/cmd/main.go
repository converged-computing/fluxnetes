package main

import (
	"flag"
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/converged-computing/fluxnetes/pkg/fluxion"
	pb "github.com/converged-computing/fluxnetes/pkg/fluxion-grpc"
	"github.com/converged-computing/fluxnetes/pkg/service"
	svcPb "github.com/converged-computing/fluxnetes/pkg/service-grpc"
)

const (
	defaultPort           = ":4242"
	enableExternalService = false
)

var responsechan chan string

func main() {
	fmt.Println("This is the fluxion grpc server")
	policy := flag.String("policy", "", "Match policy")
	label := flag.String("label", "", "Label name for fluxnetes dedicated nodes")
	grpcPort := flag.String("port", defaultPort, "Port for grpc service")
	enableServicePlugin := flag.Bool("external-service", enableExternalService, "Flag to enable the external service (defaults to false)")

	flag.Parse()

	// Ensure our port starts with :
	port := *grpcPort
	if !strings.HasPrefix(":", port) {
		port = fmt.Sprintf(":%s", port)
	}

	// Fluxion GRPC
	flux := fluxion.Fluxion{}
	flux.InitFluxion(*policy, *label)

	lis, err := net.Listen("tcp", port)
	if err != nil {
		fmt.Printf("[GRPCServer] failed to listen: %v\n", err)
	}

	responsechan = make(chan string)
	server := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
		}),
	)
	pb.RegisterFluxionServiceServer(server, &flux)

	// External plugin (Kubectl) GRPC
	// This will eventually be an external GRPC module that can
	// be shared by fluxnetes (flux-k8s) and fluxnetes-kubectl
	// We give it a handle to Flux to get the state of groups
	// and job Ids. The direct interaction with Fluxion
	// happens through the other service handle
	if *enableServicePlugin {
		plugin := service.ExternalService{}
		plugin.Init()
		svcPb.RegisterExternalPluginServiceServer(server, &plugin)
	}

	fmt.Printf("[GRPCServer] gRPC Listening on %s\n", lis.Addr().String())
	err = server.Serve(lis)
	if err != nil {
		fmt.Printf("[GRPCServer] failed to serve: %v\n", err)
	}

	flux.Close()
	fmt.Printf("[GRPCServer] Exiting\n")
}
