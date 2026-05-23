CREATE TABLE IF NOT EXISTS skill_polls (
  skill_name TEXT PRIMARY KEY,
  etag TEXT,
  last_checked_at TEXT,
  last_commit TEXT
);
