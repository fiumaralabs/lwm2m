CREATE TABLE jobs (tenant TEXT NOT NULL, id TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY (tenant, id));
CREATE TABLE job_tasks (tenant TEXT NOT NULL, job_id TEXT NOT NULL, device TEXT NOT NULL, payload BLOB NOT NULL, PRIMARY KEY (tenant, job_id, device));
PRAGMA user_version=17;
