CREATE TABLE webhooks (
    tenant TEXT NOT NULL,
    id TEXT NOT NULL,
    body BLOB NOT NULL,
    PRIMARY KEY (tenant, id)
);
CREATE TABLE webhook_replays (
    tenant TEXT NOT NULL,
    target TEXT NOT NULL,
    key TEXT NOT NULL,
    body BLOB NOT NULL,
    expires INTEGER NOT NULL,
    PRIMARY KEY (tenant, target, key)
);
CREATE INDEX webhook_replays_expiry ON webhook_replays(expires);
CREATE TABLE settings (
    tenant TEXT PRIMARY KEY NOT NULL,
    body BLOB NOT NULL
);
PRAGMA user_version = 4;
