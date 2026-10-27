CREATE TABLE devices (tenant TEXT NOT NULL, endpoint TEXT NOT NULL, payload TEXT NOT NULL, last_seen TEXT NOT NULL DEFAULT '', PRIMARY KEY (tenant, endpoint));
PRAGMA user_version = 1;
