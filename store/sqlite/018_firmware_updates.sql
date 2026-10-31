CREATE TABLE firmware_updates (
    tenant TEXT NOT NULL,
    id TEXT NOT NULL,
    device TEXT NOT NULL,
    status TEXT NOT NULL,
    payload BLOB NOT NULL,
    PRIMARY KEY (tenant, id)
);
CREATE UNIQUE INDEX firmware_updates_active ON firmware_updates(tenant, device)
    WHERE status NOT IN ('succeeded', 'failed', 'cancelled');
PRAGMA user_version=18;
