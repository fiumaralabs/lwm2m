CREATE TABLE IF NOT EXISTS operation_job_steps (tenant TEXT NOT NULL, operation_id TEXT NOT NULL, step INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (tenant, operation_id));
