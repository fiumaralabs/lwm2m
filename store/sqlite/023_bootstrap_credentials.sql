CREATE TABLE IF NOT EXISTS bootstrap_credentials (
    tenant TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    identity TEXT NOT NULL UNIQUE,
    key BLOB,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (tenant, endpoint)
);
PRAGMA user_version=23;
