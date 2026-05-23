CREATE TABLE IF NOT EXISTS skills (
  name TEXT PRIMARY KEY,
  summary TEXT,
  categories JSON,
  tags JSON,
  compatibility_mode TEXT,
  compatibility_data JSON,
  requirements JSON,
  origin JSON,
  fingerprint TEXT,
  added_at TEXT,
  updated_at TEXT
);

CREATE TABLE IF NOT EXISTS projects (
  slug TEXT PRIMARY KEY,
  path TEXT UNIQUE,
  name TEXT,
  categories JSON,
  tags JSON,
  harnesses JSON,
  last_synced TEXT
);

CREATE TABLE IF NOT EXISTS installs (
  skill_name TEXT,
  project_slug TEXT,
  version TEXT,
  harnesses JSON,
  installed_at TEXT,
  PRIMARY KEY (skill_name, project_slug)
);

CREATE TABLE IF NOT EXISTS updates (
  skill_name TEXT PRIMARY KEY,
  from_version TEXT,
  to_version TEXT,
  source TEXT,
  detected_at TEXT,
  summary_status TEXT,
  summary_path TEXT,
  status TEXT
);

CREATE TABLE IF NOT EXISTS invocations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  skill_name TEXT,
  project_slug TEXT,
  harness TEXT,
  trigger TEXT,
  invoked_at TEXT,
  source TEXT
);

CREATE INDEX IF NOT EXISTS idx_invocations_skill_date
  ON invocations (skill_name, invoked_at);

CREATE TABLE IF NOT EXISTS detected (
  path TEXT PRIMARY KEY,
  skill_name TEXT,
  detected_at TEXT,
  source_guess TEXT,
  action TEXT
);

CREATE TABLE IF NOT EXISTS requirement_checks (
  skill_name TEXT,
  requirement_type TEXT,
  requirement_name TEXT,
  status TEXT,
  checked_at TEXT,
  detail TEXT,
  PRIMARY KEY (skill_name, requirement_type, requirement_name)
);
