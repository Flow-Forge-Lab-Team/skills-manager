CREATE TABLE IF NOT EXISTS discovery_drift_reviews (
  group_id TEXT PRIMARY KEY,
  status TEXT,
  reason TEXT,
  canonical_installation_id TEXT,
  reviewed_at TEXT
);
