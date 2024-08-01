package utils

// PodSpec is a temporary holder for the protobuf
// variant that it will be converted to. We could
// remove it, but since we need to refactor to use
// an entire group I'm leaving for now
type PodSpec struct {
	Id        string
	Container string
	Cpu       int32
	Memory    int64
	Gpu       int64
	Storage   int64
	Labels    []string
}
