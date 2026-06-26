ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS agentd_url text;
ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS host_key_fingerprint text;
ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS observed_host_key_fingerprint text;
ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS host_key_verified boolean NOT NULL DEFAULT false;
ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS forced_command text;
ALTER TABLE executor_nodes ADD COLUMN IF NOT EXISTS verified_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_executor_nodes_kind_verified
  ON executor_nodes(kind, host_key_verified, status);
