ALTER TABLE security ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
PRAGMA user_version = 15;
