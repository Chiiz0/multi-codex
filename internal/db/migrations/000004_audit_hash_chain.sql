ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash text NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS entry_hash text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_audit_logs_entry_hash
  ON audit_logs(entry_hash)
  WHERE entry_hash <> '';
