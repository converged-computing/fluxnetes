package strategy

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	groups "k8s.io/kubernetes/pkg/scheduler/framework/plugins/fluxnetes/group"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Interface for a queue strategy
// A queue strategy both controls a work function (what the worker does, and arguments)
// Along with how to orchestrate the last part of the schedule loop, schedule, which
// moves pods from provisional (waiting for groups to be ready) into worker queues

// We currently just return a name, and provide a schedule function to move things around!
type QueueStrategy interface {
	Name() string

	// provide the entire queue to interact with
	Schedule(context.Context, *pgxpool.Pool, int32) ([]river.InsertManyParams, error)
	AddWorkers(*river.Workers)
	Enqueue(context.Context, *pgxpool.Pool, *corev1.Pod, *groups.PodGroup) error
	PostSubmit(context.Context, *pgxpool.Pool, *river.Client[pgx.Tx]) error

	// Return metadata about the strategy for the Queue to know
	GetReservationDepth() int32
}
