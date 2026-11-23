CREATE TABLE IF NOT EXISTS device_replays (
    tenant TEXT NOT NULL,
    key TEXT NOT NULL,
    status INTEGER NOT NULL,
    header BLOB NOT NULL,
    body BLOB NOT NULL,
    expires INTEGER NOT NULL,
    PRIMARY KEY (tenant, key)
);
