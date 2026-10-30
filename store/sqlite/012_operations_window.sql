CREATE INDEX IF NOT EXISTS operations_tenant_seq ON operations (tenant, seq);
PRAGMA user_version=12;
