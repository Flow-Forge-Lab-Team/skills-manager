CREATE TABLE IF NOT EXISTS discovery_scans (
  scan_id TEXT PRIMARY KEY,
  scanned_at TEXT,
  global_scope INTEGER,
  project_roots JSON
);

CREATE TABLE IF NOT EXISTS discovery_tools (
  tool_id TEXT PRIMARY KEY,
  display_name TEXT,
  detected INTEGER,
  status TEXT,
  global_roots JSON,
  project_patterns JSON,
  last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS discovery_projects (
  project_id TEXT PRIMARY KEY,
  root_path TEXT UNIQUE,
  repo_remote TEXT,
  detected_tools JSON,
  last_scanned_at TEXT,
  present INTEGER,
  missing_since TEXT
);

CREATE TABLE IF NOT EXISTS discovery_installations (
  installation_id TEXT PRIMARY KEY,
  skill_name TEXT,
  tool_id TEXT,
  scope TEXT,
  project_id TEXT,
  source_path TEXT,
  content_path TEXT,
  content_sha256 TEXT,
  content_size_bytes INTEGER,
  modified_at TEXT,
  managed INTEGER,
  ownership TEXT,
  format TEXT,
  present INTEGER,
  first_seen_at TEXT,
  last_seen_at TEXT,
  missing_since TEXT
);

CREATE INDEX IF NOT EXISTS idx_discovery_installations_skill_name
  ON discovery_installations (skill_name);

CREATE INDEX IF NOT EXISTS idx_discovery_installations_content_sha
  ON discovery_installations (content_sha256);

CREATE INDEX IF NOT EXISTS idx_discovery_installations_scope_present
  ON discovery_installations (scope, present);

CREATE INDEX IF NOT EXISTS idx_discovery_installations_project
  ON discovery_installations (project_id);

CREATE TABLE IF NOT EXISTS discovery_drift_groups (
  group_id TEXT PRIMARY KEY,
  group_type TEXT,
  skill_name TEXT,
  content_sha256 TEXT,
  status TEXT,
  present INTEGER,
  last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS discovery_drift_group_installations (
  group_id TEXT,
  installation_id TEXT,
  PRIMARY KEY (group_id, installation_id)
);
