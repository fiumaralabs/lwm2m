CREATE TABLE event_heads (tenant TEXT PRIMARY KEY, sequence INTEGER NOT NULL, low INTEGER NOT NULL DEFAULT 0);
CREATE TABLE events (tenant TEXT NOT NULL, sequence INTEGER NOT NULL, type TEXT NOT NULL, device TEXT NOT NULL, stamp INTEGER NOT NULL, body TEXT NOT NULL, data BLOB, PRIMARY KEY (tenant, sequence));
CREATE INDEX events_device ON events (tenant, device, sequence);
CREATE INDEX events_expiry ON events (tenant, stamp);
PRAGMA user_version = 3;
