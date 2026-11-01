CREATE TABLE IF NOT EXISTS device_create_outcomes (
    tenant TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    PRIMARY KEY (tenant, operation_id)
);
