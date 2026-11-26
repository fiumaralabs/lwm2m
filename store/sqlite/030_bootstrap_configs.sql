CREATE TABLE IF NOT EXISTS bootstrap_configs (tenant TEXT NOT NULL, endpoint TEXT NOT NULL, config BLOB NOT NULL, PRIMARY KEY (tenant, endpoint));
