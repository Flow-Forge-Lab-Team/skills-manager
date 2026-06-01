CREATE TABLE IF NOT EXISTS dashboard_action_reviews (
  recommendation_id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  reason TEXT,
  error_detail TEXT,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS dashboard_action_audit (
  audit_id INTEGER PRIMARY KEY AUTOINCREMENT,
  recommendation_id TEXT NOT NULL,
  action TEXT NOT NULL,
  status TEXT NOT NULL,
  detail TEXT,
  created_at TEXT NOT NULL
);
