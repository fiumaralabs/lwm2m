CREATE TABLE operations (tenant TEXT NOT NULL, id TEXT NOT NULL, device TEXT NOT NULL, status TEXT NOT NULL, seq INTEGER NOT NULL, body TEXT NOT NULL, PRIMARY KEY (tenant, id));
CREATE INDEX operations_pending ON operations (tenant, device, status, seq);
PRAGMA user_version = 2;
