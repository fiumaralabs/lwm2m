CREATE TABLE tenants (
    store_key TEXT PRIMARY KEY,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO tenants(store_key, payload) VALUES ('default', '{"id":"default","name":"default","status":"active","limits":{"devices":0,"operationsPerMinute":0,"eventsPerSecond":0,"firmwareStorageBytes":0,"timeseriesRetentionDays":0}}');
PRAGMA user_version = 11;
