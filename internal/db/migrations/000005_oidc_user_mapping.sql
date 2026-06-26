ALTER TABLE users ADD COLUMN IF NOT EXISTS external_provider text NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN IF NOT EXISTS external_subject text;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_external_identity
  ON users(external_provider, external_subject)
  WHERE external_subject IS NOT NULL;
