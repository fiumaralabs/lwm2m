CREATE TABLE models (
    tenant TEXT NOT NULL,
    object_id INTEGER NOT NULL,
    major INTEGER NOT NULL,
    minor INTEGER NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (tenant, object_id, major, minor)
);
PRAGMA user_version = 10;
