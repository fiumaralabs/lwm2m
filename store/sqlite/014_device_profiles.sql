CREATE TABLE device_profiles (
    tenant TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    observations BLOB,
    attributes BLOB,
    on_register BLOB,
    timeseries BLOB,
    bootstrap_template BLOB,
    PRIMARY KEY (tenant, id)
);
PRAGMA user_version = 14;
