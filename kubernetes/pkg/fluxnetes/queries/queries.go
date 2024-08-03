package queries

// Queries used by the main queue (and shared across strategies sometimes)
const (
	GetTimestampQuery = "select created_at from pods_provisional where group_name=$1 limit 1"
	GetPodQuery       = "select * from pods_provisional where group_name=$1 and namespace=$2 and name=$3"
	InsertPodQuery    = "insert into pods_provisional (podspec, namespace, name, duration, created_at, group_name, group_size) values ($1, $2, $3, $4, $5, $6, $7)"
	CountPodsQuery    = "select count(*) from pods_provisional where group_name=$1"
	UpdateNodesQuery  = "update river_job set args = jsonb_set(args, '{nodes}', to_jsonb($1::text)) where id=$2;"

	// Reservations
	AddReservationQuery     = "insert into reservations (group_name, flux_id) values ($1, $2);"
	DeleteReservationsQuery = "truncate reservations; delete from reservations;"
	GetReservationsQuery    = "select (group_name, flux_id) from reservations;"

	// This query should achieve the following (but does not work)
	// 1. Select groups for which the size >= the number of pods we've seen
	// 2. Then get the group_name, group_size, and podspec for each (this goes to scheduler)
	// Ensures we are sorting by the timestamp when they were added (should be DESC I think)
	RefreshGroupsQuery     = "refresh materialized view groups_size;"
	SelectGroupsReadyQuery = "select * from pods_provisional join groups_size on pods_provisional.group_name = groups_size.group_name where  group_size >= count order by created_at desc;"

	// 3. Then delete all from the table
	DeleteGroupsQuery = "delete from pods_provisional where group_name in ('%s');"

	// Note that is used to be done with two queries - these are no longer used
	SelectGroupsAtSizeQuery = "select group_name from pods_provisional group by group_name, group_size, created_at having group_size >= count(*) order by created_at desc;"
	SelectGroupsQuery       = "select group_name, group_size, podspec, duration from pods_provisional where group_name in ('%s');"
)
