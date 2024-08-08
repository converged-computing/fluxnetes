package types

// EnqueueStatus is returned by the provisional enqueue to provide context
// to the calling queue about what action to take
type EnqueueStatus int

const (
	// If a pod is already in provisional, group provisional, and pending
	PodEnqueueSuccess EnqueueStatus = iota + 1

	// The pod has already been moved into pending (and submit, but not complete)
	// and we do not accept new pods for the group
	GroupAlreadyInPending

	// The pod is invalid (podspec cannot serialize, etc) and should be discarded
	PodInvalid

	// Unknown means some other error happened (usually not related to pod)
	Unknown
)
