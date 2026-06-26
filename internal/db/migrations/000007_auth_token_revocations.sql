CREATE TABLE IF NOT EXISTS auth_token_revocations (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  token_hash text NOT NULL UNIQUE,
  actor_id text NOT NULL DEFAULT '',
  subject text NOT NULL DEFAULT '',
  reason text NOT NULL DEFAULT 'logout',
  revoked_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_auth_token_revocations_expires_at
  ON auth_token_revocations(expires_at);
