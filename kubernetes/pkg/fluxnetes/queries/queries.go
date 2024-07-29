package queries

// Queries used by the main queue (and shared across strategies sometimes)
const (
	GetTimestampQuery = "select created_at from pods_provisional where group_name=$1 limit 1"
	GetPodQuery       = "select * from pods_provisional where group_name=$1 and namespace=$2 and name=$3"
	InsertPodQuery    = "insert into pods_provisional (podspec, namespace, name, created_at, group_name, group_size) values ($1, $2, $3, $4, $5, $6)"
	CountPodsQuery    = "select count(*) from pods_provisional where group_name=$1"
	UpdateNodesQuery  = "update river_job set args = jsonb_set(args, '{nodes}', to_jsonb($1::text)) where id=$2;"

	// This could use improvement from someone good at SQL. We need to:
	// 1. Select groups for which the size >= the number of pods we've seen
	// 2. Then get the group_name, group_size, and podspec for each (this goes to scheduler)
	// 3. Delete all from the table
	// Ensure we are sorting by the timestamp when they were added (should be DESC I think)
	SelectGroupsAtSizeQuery = "select group_name from pods_provisional group by group_name, group_size having group_size >= count(*);"
	SelectGroupsQuery       = "select group_name, group_size, podspec from pods_provisional where group_name in ('%s');"
	DeleteGroupsQuery       = "delete from pods_provisional where group_name in ('%s');"
)
