CREATE TABLE IF NOT EXISTS mcp_sessions (
  id text PRIMARY KEY,
  actor_id text NOT NULL DEFAULT '',
  protocol_version text NOT NULL DEFAULT '2025-06-18',
  status text NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL,
  last_event_id bigint NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS mcp_session_events (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  session_id text NOT NULL REFERENCES mcp_sessions(id) ON DELETE CASCADE,
  seq bigint NOT NULL,
  event_type text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_mcp_sessions_expires_at
  ON mcp_sessions(expires_at);

CREATE INDEX IF NOT EXISTS idx_mcp_session_events_session_seq
  ON mcp_session_events(session_id, seq);
