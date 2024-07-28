package strategy

import (
	"context"

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
	Schedule(ctx context.Context, pool *pgxpool.Pool) ([]river.InsertManyParams, error)
	AddWorkers(*river.Workers)
}
