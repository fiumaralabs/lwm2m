CREATE TABLE bootstrap_sessions (seq INTEGER PRIMARY KEY AUTOINCREMENT, tenant TEXT NOT NULL, endpoint TEXT NOT NULL, payload BLOB NOT NULL);
CREATE INDEX bootstrap_sessions_device ON bootstrap_sessions(tenant, endpoint, seq DESC);
PRAGMA user_version=19;
