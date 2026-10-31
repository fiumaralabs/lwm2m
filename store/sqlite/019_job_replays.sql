CREATE TABLE IF NOT EXISTS job_replays (tenant TEXT NOT NULL, key TEXT NOT NULL, body BLOB NOT NULL, expires INTEGER NOT NULL, PRIMARY KEY (tenant, key));
PRAGMA user_version=19;
