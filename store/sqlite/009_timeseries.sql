CREATE TABLE timeseries_points (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    path TEXT NOT NULL,
    seconds INTEGER NOT NULL,
    nanos INTEGER NOT NULL,
    value_type TEXT NOT NULL,
    value TEXT NOT NULL
);
CREATE INDEX timeseries_lookup ON timeseries_points(tenant, endpoint, path, seconds, nanos, sequence);
PRAGMA user_version = 9;
