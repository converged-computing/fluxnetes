CREATE TABLE pods_provisional (
    podspec TEXT NOT NULL,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL, 
    created_at timestamptz NOT NULL default NOW(),
    group_name TEXT NOT NULL,
    group_size INTEGER NOT NULL
);
CREATE INDEX group_name_index ON pods_provisional (group_name);
