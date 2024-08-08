package queries

const (
	// Used to get the earliest timstamp for the group
	GetTimestampQuery = "select created_at from pods_provisional where group_name=$1 and namespace=$2 limit 1;"

	// When we complete a job worker type after a successful MatchAllocate, this is how we send nodes back via an event
	UpdateNodesQuery = "update river_job set args = jsonb_set(args, '{nodes}', to_jsonb($1::text)) where id=$2;"

	// Reservations
	AddReservationQuery     = "insert into reservations (group_name, flux_id) values ($1, $2);"
	DeleteReservationsQuery = "truncate reservations; delete from reservations;"
	GetReservationsQuery    = "select (group_name, flux_id) from reservations;"

	// This query should achieve the following
	// 1. Select groups for which the size >= the number of pods we've seen
	// 2. Then get a representative pod to model the resources for the group
	// TODO add back created by and then sort by it desc
	SelectGroupsAtSizeQuery = "select group_name, group_size, duration, podspec, namespace from groups_provisional where current_size >= group_size;"

	// This currently will use one podspec (and all names) and we eventually want it to use all podspecs
	SelectPodsQuery = `select name, podspec from pods_provisional where group_name = $1 and namespace = $2;`

	// Pending queue - inserted after moving from provisional
	InsertIntoPending = "insert into pending_queue (group_name, namespace, group_size) SELECT '%s', '%s', '%d' WHERE NOT EXISTS (SELECT (group_name, namespace) FROM pending_queue WHERE group_name = '%s' and namespace = '%s');"

	// We delete from the provisional tables when a group is added to the work queues (and pending queue, above)
	DeleteProvisionalGroupsQuery = "delete from groups_provisional where %s;"
	DeleteGroupsQuery            = "delete from pods_provisional where %s;"

	// TODO add created_at back
	InsertIntoProvisionalQuery = "insert into pods_provisional (podspec, namespace, name, duration, group_name) select '%s', '%s', '%s', %d, '%s' where not exists (select (group_name, name, namespace) from pods_provisional where group_name = '%s' and namespace = '%s' and name = '%s');"

	// Enqueue queries
	// 1. Single pods are added to the pods_provisional - this is how we track uniqueness (and eventually will grab all podspecs from here)
	// 2. Groups are added to the groups_provisional, and this is where we can easily store a current cound
	// Note that we add a current_size of 1 here assuming the first creation is done paired with an existing pod (and then don't need to increment again)
	InsertIntoGroupProvisional = "insert into groups_provisional (group_name, namespace, group_size, duration, podspec, current_size) select '%s', '%s', '%d', '%d', '%s', '1' WHERE NOT EXISTS (SELECT (group_name, namespace) FROM groups_provisional WHERE group_name = '%s' and namespace = '%s');"
	IncrementGroupProvisional  = "update groups_provisional set current_size = current_size + 1 where group_name = '%s' and namespace = '%s';"

	// Pending Queue queries
	// 3. We always check if a group is in pending before Enqueue, because if so, we aren't allowed to modify / add to the group
	IsPendingQuery = "select * from pending_queue where group_name = $1 and namespace = $2;"

	// We remove from pending to allow another group submission of the same name on cleanup
	DeleteFromPendingQuery = "delete from pending_queue where group_name=$1 and namespace=$2;"
)
