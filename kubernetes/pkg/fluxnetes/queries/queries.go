package queries

// Queries used by the main queue (and shared across strategies sometimes)
const (
	GetTimestampQuery = "select created_at from pods_provisional where group_name=$1 limit 1"
	SelectGroupsQuery = "select group_name, group_size, podspec from pods_provisional"
	GetPodQuery       = "select * from pods_provisional where group_name=$1 and namespace=$2 and name=$3"
	InsertPodQuery    = "insert into pods_provisional (podspec, namespace, name, created_at, group_name, group_size) values ($1, $2, $3, $4, $5, $6)"
	CountPodsQuery    = "select count(*) from pods_provisional where group_name=$1"
)
