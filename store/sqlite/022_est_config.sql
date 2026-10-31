CREATE TABLE IF NOT EXISTS est_configs (tenant TEXT PRIMARY KEY, enabled INTEGER NOT NULL, certificate TEXT NOT NULL, private_key TEXT NOT NULL, validity_days INTEGER NOT NULL, selector TEXT NOT NULL);
PRAGMA user_version=22;
