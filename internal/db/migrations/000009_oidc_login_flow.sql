ALTER TABLE auth_sessions
  ADD COLUMN IF NOT EXISTS external_session_id text;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_external_session_active
  ON auth_sessions(external_provider, external_session_id)
  WHERE external_session_id IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_auth_sessions_subject_active
  ON auth_sessions(external_provider, external_subject)
  WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS auth_login_states (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  state_hash text NOT NULL UNIQUE,
  nonce_hash text NOT NULL,
  code_verifier text NOT NULL,
  return_to text NOT NULL DEFAULT '/',
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  consumed_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_auth_login_states_expires_at
  ON auth_login_states(expires_at);
