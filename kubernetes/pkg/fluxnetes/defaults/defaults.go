package defaults

import (
	"math"
)

const (
	// https://github.com/riverqueue/river/discussions/475
	// The database column is an int16
	MaxAttempts = math.MaxInt16

	// Default duration is 3600 seconds (one hour)
	DefaultDuration = 3600
)
