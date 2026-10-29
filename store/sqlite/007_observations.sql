CREATE TABLE observations (
    tenant TEXT NOT NULL,
    id TEXT NOT NULL,
    device TEXT NOT NULL,
    body TEXT NOT NULL,
    PRIMARY KEY (tenant, id)
);
CREATE INDEX observations_device ON observations(tenant, device, id);
PRAGMA user_version = 7;
