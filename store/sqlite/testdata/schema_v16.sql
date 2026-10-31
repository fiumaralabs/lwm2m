-- SQLite migrations from main at 844e57a67cd60020ac77e513cb2736ce56c12de4.
-- store/sqlite/001_devices.sql
CREATE TABLE devices (tenant TEXT NOT NULL, endpoint TEXT NOT NULL, payload TEXT NOT NULL, last_seen TEXT NOT NULL DEFAULT '', PRIMARY KEY (tenant, endpoint));
PRAGMA user_version = 1;

-- store/sqlite/002_operations.sql
CREATE TABLE operations (tenant TEXT NOT NULL, id TEXT NOT NULL, device TEXT NOT NULL, status TEXT NOT NULL, seq INTEGER NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, id));
CREATE INDEX operations_pending ON operations (tenant, device, status, seq);
PRAGMA user_version = 2;

-- store/sqlite/003_events.sql
CREATE TABLE event_heads (tenant TEXT PRIMARY KEY, sequence INTEGER NOT NULL, low INTEGER NOT NULL DEFAULT 0);
CREATE TABLE events (tenant TEXT NOT NULL, sequence INTEGER NOT NULL, type TEXT NOT NULL, device TEXT NOT NULL, stamp INTEGER NOT NULL, body TEXT NOT NULL, data BLOB, PRIMARY KEY (tenant, sequence));
CREATE INDEX events_device ON events (tenant, device, sequence);
CREATE INDEX events_expiry ON events (tenant, stamp);
PRAGMA user_version = 3;

-- store/sqlite/004_webhooks_settings.sql
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

-- store/sqlite/005_api_keys.sql
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY NOT NULL,
    tenant TEXT NOT NULL,
    body BLOB NOT NULL
);
CREATE INDEX api_keys_tenant_id ON api_keys(tenant, id);
PRAGMA user_version = 5;

-- store/sqlite/006_security.sql
CREATE TABLE security (
    endpoint TEXT PRIMARY KEY NOT NULL,
    psk_identity TEXT NOT NULL,
    psk_key BLOB,
    x509 INTEGER NOT NULL
);
CREATE UNIQUE INDEX security_psk_identity ON security(psk_identity) WHERE psk_identity <> '';
PRAGMA user_version = 6;

-- store/sqlite/007_observations.sql
CREATE TABLE observations (
    tenant TEXT NOT NULL,
    id TEXT NOT NULL,
    device TEXT NOT NULL,
    body TEXT NOT NULL,
    PRIMARY KEY (tenant, id)
);
CREATE INDEX observations_device ON observations(tenant, device, id);
PRAGMA user_version = 7;

-- store/sqlite/008_audit.sql
CREATE TABLE audit_log (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant TEXT NOT NULL,
    body TEXT NOT NULL
);
CREATE INDEX audit_log_tenant_sequence ON audit_log(tenant, sequence);
PRAGMA user_version = 8;

-- store/sqlite/009_timeseries.sql
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

-- store/sqlite/010_models.sql
CREATE TABLE models (
    tenant TEXT NOT NULL,
    object_id INTEGER NOT NULL,
    major INTEGER NOT NULL,
    minor INTEGER NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (tenant, object_id, major, minor)
);
PRAGMA user_version = 10;

-- store/sqlite/011_tenants.sql
CREATE TABLE tenants (
    store_key TEXT PRIMARY KEY,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
INSERT INTO tenants(store_key, payload) VALUES ('default', '{"id":"default","name":"default","status":"active","limits":{"devices":0,"operationsPerMinute":0,"eventsPerSecond":0,"firmwareStorageBytes":0,"timeseriesRetentionDays":0}}');
PRAGMA user_version = 11;

-- store/sqlite/012_operations_window.sql
CREATE INDEX IF NOT EXISTS operations_tenant_seq ON operations (tenant, seq);
PRAGMA user_version=12;

-- store/sqlite/013_firmware.sql
CREATE TABLE firmware (tenant TEXT NOT NULL, id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (tenant, id));
PRAGMA user_version=13;

-- store/sqlite/014_device_profiles.sql
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

-- store/sqlite/015_security_updated_at.sql
ALTER TABLE security ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
PRAGMA user_version = 15;

-- store/sqlite/016_device_profile_replays.sql
CREATE TABLE IF NOT EXISTS device_profile_replays (tenant TEXT NOT NULL, key TEXT NOT NULL, body BLOB NOT NULL, expires INTEGER NOT NULL, PRIMARY KEY (tenant, key));
PRAGMA user_version = 16;
