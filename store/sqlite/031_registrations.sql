CREATE TABLE IF NOT EXISTS registrations (
    id TEXT PRIMARY KEY,
    endpoint TEXT NOT NULL UNIQUE,
    payload BLOB NOT NULL
);
