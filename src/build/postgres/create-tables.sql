CREATE TABLE pods_provisional (
    podspec TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL, 
    created_at timestamptz NOT NULL default NOW(),
    group_name TEXT NOT NULL,
    group_size INTEGER NOT NULL
);
CREATE INDEX group_name_index ON pods_provisional (group_name);

-- REFRESH MATERIALIZED VIEW group_sizes;
CREATE materialized view groups_size as SELECT count(*), group_name FROM pods_provisional group by group_name;
-- We only need the fluxid for a reservation
CREATE TABLE reservations (
    group_name TEXT NOT NULL,
    flux_id INTEGER NOT NULL
);
