CREATE TABLE api_keys (
    id TEXT PRIMARY KEY NOT NULL,
    tenant TEXT NOT NULL,
    body BLOB NOT NULL
);
CREATE INDEX api_keys_tenant_id ON api_keys(tenant, id);
PRAGMA user_version = 5;
