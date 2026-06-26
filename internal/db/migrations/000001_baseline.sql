CREATE TYPE task_status AS ENUM (
  'draft',
  'validated',
  'queued',
  'running',
  'blocked',
  'failed',
  'completed',
  'cancelled'
);

CREATE TYPE run_status AS ENUM (
  'queued',
  'preparing',
  'running',
  'succeeded',
  'failed',
  'blocked',
  'cancelled',
  'timed_out'
);

CREATE TYPE run_role AS ENUM (
  'main',
  'feature',
  'test',
  'audit',
  'git_sync',
  'docs',
  'release'
);

CREATE TYPE executor_kind AS ENUM ('docker', 'ssh');

CREATE TABLE organizations (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  name text NOT NULL,
  slug text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  email text NOT NULL UNIQUE,
  display_name text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);

CREATE TABLE projects (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name text NOT NULL,
  slug text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, slug)
);

CREATE TABLE repositories (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  provider text NOT NULL,
  remote_url text NOT NULL,
  default_branch text NOT NULL DEFAULT 'main',
  local_mirror_path text,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE skills (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  enabled boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);

CREATE TABLE skill_versions (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  skill_id uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
  version text NOT NULL,
  content_hash text NOT NULL,
  path text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (skill_id, version)
);

CREATE TABLE agent_profiles (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid REFERENCES projects(id) ON DELETE CASCADE,
  name text NOT NULL,
  role run_role NOT NULL,
  model text NOT NULL,
  sandbox_mode text NOT NULL,
  approval_policy text NOT NULL,
  executor executor_kind NOT NULL,
  image text,
  network_enabled boolean NOT NULL DEFAULT false,
  config jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE executor_nodes (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
  kind executor_kind NOT NULL,
  name text NOT NULL,
  address text,
  labels jsonb NOT NULL DEFAULT '{}',
  capacity jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL DEFAULT 'active',
  last_seen_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tasks (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
  task_key text NOT NULL,
  title text NOT NULL,
  status task_status NOT NULL DEFAULT 'draft',
  envelope jsonb NOT NULL,
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project_id, task_key)
);

CREATE TABLE task_dependencies (
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  depends_on_task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, depends_on_task_id)
);

CREATE TABLE runs (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  role run_role NOT NULL,
  status run_status NOT NULL DEFAULT 'queued',
  executor executor_kind NOT NULL,
  executor_node_id uuid REFERENCES executor_nodes(id),
  branch text,
  worktree_path text,
  skill_version_id uuid REFERENCES skill_versions(id),
  agent_profile_id uuid REFERENCES agent_profiles(id),
  started_at timestamptz,
  finished_at timestamptz,
  result jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE run_events (
  id bigserial PRIMARY KEY,
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  seq bigint NOT NULL,
  level text NOT NULL DEFAULT 'info',
  event_type text NOT NULL,
  message text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, seq)
);

CREATE TABLE tool_calls (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  caller text NOT NULL,
  tool_name text NOT NULL,
  input jsonb NOT NULL DEFAULT '{}',
  output jsonb NOT NULL DEFAULT '{}',
  status text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz
);

CREATE TABLE artifacts (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  kind text NOT NULL,
  name text NOT NULL,
  path text NOT NULL,
  sha256 text,
  size_bytes bigint,
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE scope_checks (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  base_ref text NOT NULL,
  status text NOT NULL,
  changed_files jsonb NOT NULL DEFAULT '[]',
  violations jsonb NOT NULL DEFAULT '[]',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE review_findings (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id uuid REFERENCES runs(id) ON DELETE SET NULL,
  severity text NOT NULL,
  category text NOT NULL,
  file_path text,
  line_start integer,
  line_end integer,
  title text NOT NULL,
  detail text NOT NULL,
  status text NOT NULL DEFAULT 'open',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE approvals (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  task_id uuid NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  requested_by uuid REFERENCES users(id),
  approved_by uuid REFERENCES users(id),
  approval_type text NOT NULL,
  status text NOT NULL DEFAULT 'pending',
  reason text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  decided_at timestamptz
);

CREATE TABLE audit_logs (
  id uuid PRIMARY KEY DEFAULT uuidv7(),
  org_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
  actor_type text NOT NULL,
  actor_id text NOT NULL,
  action text NOT NULL,
  resource_type text NOT NULL,
  resource_id text NOT NULL,
  payload jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_project_status ON tasks(project_id, status);
CREATE INDEX idx_runs_task_role ON runs(task_id, role);
CREATE INDEX idx_run_events_run_seq ON run_events(run_id, seq);
CREATE INDEX idx_audit_logs_org_created ON audit_logs(org_id, created_at DESC);
