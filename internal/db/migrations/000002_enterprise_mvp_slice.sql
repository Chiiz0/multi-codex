ALTER TABLE skills ADD COLUMN IF NOT EXISTS role run_role NOT NULL DEFAULT 'feature';

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_profiles_project_name
  ON agent_profiles(COALESCE(project_id, '00000000-0000-0000-0000-000000000000'::uuid), name);

CREATE UNIQUE INDEX IF NOT EXISTS idx_executor_nodes_org_name
  ON executor_nodes(org_id, name);

CREATE INDEX IF NOT EXISTS idx_artifacts_run_kind ON artifacts(run_id, kind);
CREATE INDEX IF NOT EXISTS idx_approvals_task_status ON approvals(task_id, status);
CREATE INDEX IF NOT EXISTS idx_scope_checks_task_created ON scope_checks(task_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tool_calls_created ON tool_calls(created_at DESC);
