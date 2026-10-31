CREATE TABLE IF NOT EXISTS trust_anchors (tenant TEXT NOT NULL, id TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY (tenant, id));
CREATE TABLE IF NOT EXISTS trust_anchor_fingerprints (tenant TEXT NOT NULL, fingerprint TEXT NOT NULL, anchor_id TEXT NOT NULL, PRIMARY KEY (tenant, fingerprint));
CREATE TABLE IF NOT EXISTS trust_anchor_replays (tenant TEXT NOT NULL, key TEXT NOT NULL, payload BLOB NOT NULL, expires INTEGER NOT NULL, PRIMARY KEY (tenant, key));
PRAGMA user_version=21;
