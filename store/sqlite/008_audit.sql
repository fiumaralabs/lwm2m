CREATE TABLE audit_log (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant TEXT NOT NULL,
    body TEXT NOT NULL
);
CREATE INDEX audit_log_tenant_sequence ON audit_log(tenant, sequence);
PRAGMA user_version = 8;
