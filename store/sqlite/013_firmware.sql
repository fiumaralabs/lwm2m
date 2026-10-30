CREATE TABLE firmware (tenant TEXT NOT NULL, id TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (tenant, id));
PRAGMA user_version=13;
