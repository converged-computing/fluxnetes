CREATE TABLE pods_provisional (
    podspec TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL, 
    duration INTEGER NOT NULL,
    created_at timestamptz NOT NULL default NOW(),
    group_name TEXT NOT NULL
);
CREATE UNIQUE INDEX group_name_index ON pods_provisional (group_name, namespace, name);

-- A single row for each group
CREATE TABLE groups_provisional (
    podspec TEXT NOT NULL,
    namespace TEXT NOT NULL,
    duration INTEGER NOT NULL,
    created_at timestamptz NOT NULL default NOW(),
    group_name TEXT NOT NULL,
    group_size INTEGER NOT NULL,
    current_size INTEGER NOT NULL
);
CREATE UNIQUE INDEX groups_provisional_index ON groups_provisional (group_name, namespace);

-- We only need the fluxid for a reservation
CREATE TABLE reservations (
    group_name TEXT NOT NULL,
    flux_id INTEGER NOT NULL
);
-- Pods get moved from provisional to pending as group objects
-- The pending queue includes states pending (still waiting to run),
CREATE TABLE pending_queue (
   group_name TEXT NOT NULL,
   namespace TEXT NOT NULL,
   group_size INTEGER NOT NULL,
   flux_id INTEGER
);
 -- Don't allow inserting the same group name / namespace stwice
CREATE UNIQUE INDEX pending_key ON pending_queue(group_name, namespace);