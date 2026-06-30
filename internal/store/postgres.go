package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

type PostgresStore struct {
	db          *sql.DB
	log         *slog.Logger
	databaseURL string
}

func NewPostgresStore(db *sql.DB, log *slog.Logger, databaseURL ...string) *PostgresStore {
	value := ""
	if len(databaseURL) > 0 {
		value = databaseURL[0]
	}
	return &PostgresStore{db: db, log: log, databaseURL: value}
}

func (s *PostgresStore) EnsureSeed(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO organizations (id, name, slug)
VALUES ($1::uuid, 'Default Organization', 'default')
ON CONFLICT (slug) DO NOTHING`, defaultOrgID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO users (id, email, display_name)
VALUES ($1::uuid, 'local-dev@multi-codex.invalid', 'Local Developer')
ON CONFLICT (email) DO NOTHING`, defaultUserID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO memberships (org_id, user_id, role)
VALUES ($1::uuid, $2::uuid, 'owner')
ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`, defaultOrgID, defaultUserID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO projects (id, org_id, name, slug, description)
VALUES ($2::uuid, $1::uuid, 'Demo Engineering', 'demo-engineering', 'Seed project for local multi-codex development.')
ON CONFLICT (org_id, slug) DO NOTHING`, defaultOrgID, defaultProjectID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO repositories (id, project_id, name, provider, remote_url, default_branch)
VALUES ($1::uuid, $2::uuid, 'demo-service', 'local', 'file:///workspace/demo-service.git', 'main')
ON CONFLICT DO NOTHING`, defaultRepoID, defaultProjectID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, seed := range seedSkillVersions(now) {
		var skillID string
		if err := s.db.QueryRowContext(ctx, `
INSERT INTO skills (org_id, name, description, role, enabled)
VALUES ($1::uuid, $2, $3, $4, $5)
ON CONFLICT (org_id, name) DO UPDATE
SET description = EXCLUDED.description,
    role = EXCLUDED.role,
    enabled = EXCLUDED.enabled
RETURNING id::text`,
			defaultOrgID, seed.Skill.Name, seed.Skill.Description, seed.Skill.Role, seed.Skill.Enabled,
		).Scan(&skillID); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO skill_versions (skill_id, version, content_hash, path)
VALUES ($1::uuid, $2, $3, $4)
ON CONFLICT (skill_id, version) DO UPDATE
SET content_hash = EXCLUDED.content_hash,
    path = EXCLUDED.path`,
			skillID, seed.Version.Version, seed.Version.ContentHash, seed.Version.Path); err != nil {
			return err
		}
	}

	for _, profile := range seedProfiles(defaultProjectID, now) {
		configBytes, _ := json.Marshal(profile.Config)
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO agent_profiles (project_id, name, role, model, sandbox_mode, approval_policy, executor, image, network_enabled, config)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT DO NOTHING`,
			profile.ProjectID, profile.Name, dbRole(profile.Role), profile.Model, profile.SandboxMode, profile.ApprovalPolicy, profile.Executor, profile.Image, profile.NetworkEnabled, configBytes); err != nil {
			return err
		}
	}

	for _, node := range seedNodes(now) {
		labelsBytes, _ := json.Marshal(node.Labels)
		capacityBytes, _ := json.Marshal(node.Capacity)
		if _, err := s.db.ExecContext(ctx, `
INSERT INTO executor_nodes (id, org_id, kind, name, address, agentd_url, host_key_fingerprint, host_key_verified, forced_command, labels, capacity, status, last_seen_at)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (id) DO UPDATE
SET kind = EXCLUDED.kind,
    name = EXCLUDED.name,
    address = EXCLUDED.address,
    agentd_url = EXCLUDED.agentd_url,
    host_key_fingerprint = EXCLUDED.host_key_fingerprint,
    forced_command = EXCLUDED.forced_command,
    labels = EXCLUDED.labels,
    capacity = EXCLUDED.capacity,
    status = EXCLUDED.status,
    last_seen_at = now()`,
			node.ID, defaultOrgID, node.Kind, node.Name, node.Address, node.AgentDURL, node.HostKeyFingerprint, node.HostKeyVerified, node.ForcedCommand, labelsBytes, capacityBytes, node.Status); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) ListProjects() []domain.Project {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, org_id::text, name, slug, description, created_at
FROM projects
ORDER BY created_at ASC`)
	if err != nil {
		s.log.Error("list projects failed", "error", err)
		return nil
	}
	defer rows.Close()

	projects := []domain.Project{}
	for rows.Next() {
		var project domain.Project
		if err := rows.Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description, &project.CreatedAt); err != nil {
			s.log.Error("scan project failed", "error", err)
			return projects
		}
		projects = append(projects, project)
	}
	return projects
}

func (s *PostgresStore) CreateProject(project domain.Project) domain.Project {
	ctx, cancel := storeContext()
	defer cancel()

	if project.Description == "" {
		project.Description = ""
	}
	if project.OrgID == "" {
		project.OrgID = defaultOrgID
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO projects (org_id, name, slug, description)
VALUES ($1, $2, $3, $4)
RETURNING id::text, org_id::text, name, slug, description, created_at`,
		project.OrgID, project.Name, project.Slug, project.Description,
	).Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description, &project.CreatedAt)
	if err != nil {
		s.log.Error("create project failed", "error", err)
	}
	return project
}

func (s *PostgresStore) GetProject(id string) (domain.Project, error) {
	if !validUUIDText(id) {
		return domain.Project{}, ErrNotFound
	}
	ctx, cancel := storeContext()
	defer cancel()

	var project domain.Project
	err := s.db.QueryRowContext(ctx, `
SELECT id::text, org_id::text, name, slug, description, created_at
FROM projects
WHERE id = $1`, id).Scan(&project.ID, &project.OrgID, &project.Name, &project.Slug, &project.Description, &project.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	return project, err
}

func (s *PostgresStore) ListRepositories(projectID string) []domain.Repository {
	if !validUUIDText(projectID) {
		return []domain.Repository{}
	}
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, project_id::text, name, provider, remote_url, default_branch, created_at
FROM repositories
WHERE project_id = $1
ORDER BY created_at ASC`, projectID)
	if err != nil {
		s.log.Error("list repositories failed", "error", err)
		return nil
	}
	defer rows.Close()

	repos := []domain.Repository{}
	for rows.Next() {
		var repo domain.Repository
		if err := rows.Scan(&repo.ID, &repo.ProjectID, &repo.Name, &repo.Provider, &repo.RemoteURL, &repo.DefaultBranch, &repo.CreatedAt); err != nil {
			s.log.Error("scan repository failed", "error", err)
			return repos
		}
		repos = append(repos, repo)
	}
	return repos
}

func (s *PostgresStore) CreateRepository(repo domain.Repository) domain.Repository {
	ctx, cancel := storeContext()
	defer cancel()

	if repo.DefaultBranch == "" {
		repo.DefaultBranch = "main"
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO repositories (project_id, name, provider, remote_url, default_branch)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, project_id::text, name, provider, remote_url, default_branch, created_at`,
		repo.ProjectID, repo.Name, repo.Provider, repo.RemoteURL, repo.DefaultBranch,
	).Scan(&repo.ID, &repo.ProjectID, &repo.Name, &repo.Provider, &repo.RemoteURL, &repo.DefaultBranch, &repo.CreatedAt)
	if err != nil {
		s.log.Error("create repository failed", "error", err)
	}
	return repo
}

func (s *PostgresStore) CreateTask(envelope domain.TaskEnvelope) domain.Task {
	ctx, cancel := storeContext()
	defer cancel()

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		s.log.Error("marshal task envelope failed", "error", err)
		return domain.Task{}
	}

	var task domain.Task
	err = s.db.QueryRowContext(ctx, `
INSERT INTO tasks (project_id, repository_id, task_key, title, status, envelope)
VALUES ($1, $2, $3, $4, 'draft', $5)
RETURNING id::text, project_id::text, repository_id::text, task_key, title, status::text, envelope, created_at, updated_at`,
		envelope.ProjectID, envelope.RepositoryID, envelope.TaskID, envelope.Title, envelopeBytes,
	).Scan(&task.ID, &task.ProjectID, &task.RepositoryID, &task.TaskKey, &task.Title, &task.Status, &envelopeBytes, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		s.log.Error("create task failed", "error", err)
		return task
	}
	task.Envelope = decodeEnvelope(envelopeBytes)
	return task
}

func (s *PostgresStore) GetTask(id string) (domain.Task, error) {
	ctx, cancel := storeContext()
	defer cancel()

	task, err := s.scanTaskRow(s.db.QueryRowContext(ctx, `
SELECT id::text, project_id::text, repository_id::text, task_key, title, status::text, envelope, created_at, updated_at
FROM tasks
WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	return task, err
}

func (s *PostgresStore) ListTasks(projectID string) []domain.Task {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, project_id::text, repository_id::text, task_key, title, status::text, envelope, created_at, updated_at
FROM tasks
WHERE project_id = $1
ORDER BY created_at ASC`, projectID)
	if err != nil {
		s.log.Error("list tasks failed", "error", err)
		return nil
	}
	defer rows.Close()

	tasks := []domain.Task{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			s.log.Error("scan task failed", "error", err)
			return tasks
		}
		tasks = append(tasks, task)
	}
	return tasks
}

func (s *PostgresStore) UpdateTaskStatus(taskID string, status string) (domain.Task, error) {
	ctx, cancel := storeContext()
	defer cancel()

	task, err := s.scanTaskRow(s.db.QueryRowContext(ctx, `
UPDATE tasks
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING id::text, project_id::text, repository_id::text, task_key, title, status::text, envelope, created_at, updated_at`, taskID, status))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, ErrNotFound
	}
	return task, err
}

func (s *PostgresStore) StartRun(taskID string, role string, executor string) (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7108001002)`); err != nil {
		return domain.Run{}, err
	}
	var taskExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&taskExists); err != nil {
		return domain.Run{}, err
	}
	if !taskExists {
		return domain.Run{}, ErrNotFound
	}
	nodeID, err := s.selectExecutorNode(ctx, tx, executor)
	if err != nil {
		return domain.Run{}, err
	}
	run, err := scanRun(tx.QueryRowContext(ctx, `
INSERT INTO runs (task_id, role, status, executor, executor_node_id, started_at)
VALUES ($1, $2, 'running', $3, $4, now())
RETURNING id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at`,
		taskID, dbRole(role), executor, nullString(nodeID),
	))
	if err != nil {
		return domain.Run{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = 'running', updated_at = now() WHERE id = $1`, taskID); err != nil {
		return domain.Run{}, err
	}

	payload := map[string]any{"role": role, "executor": executor, "executor_node_id": nodeID}
	payloadBytes, _ := json.Marshal(payload)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_events (run_id, seq, level, event_type, message, payload)
VALUES ($1, 1, 'info', 'worker_spawn', 'Worker run was scheduled by the API', $2)`, run.ID, payloadBytes); err != nil {
		return domain.Run{}, err
	}

	if err := tx.Commit(); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) EnqueueRun(taskID string, role string, executor string, priority int, attempt int, maxAttempts int, reason string) (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()

	var taskExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1)`, taskID).Scan(&taskExists); err != nil {
		return domain.Run{}, err
	}
	if !taskExists {
		return domain.Run{}, ErrNotFound
	}
	result := queueResult(priority, attempt, maxAttempts, reason)
	resultBytes, _ := json.Marshal(result)
	run, err := scanRun(tx.QueryRowContext(ctx, `
INSERT INTO runs (task_id, role, status, executor, result)
VALUES ($1, $2, 'queued', $3, $4)
RETURNING id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at`,
		taskID, dbRole(role), executor, resultBytes,
	))
	if err != nil {
		return domain.Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = 'queued', updated_at = now() WHERE id = $1`, taskID); err != nil {
		return domain.Run{}, err
	}
	payloadBytes, _ := json.Marshal(map[string]any{
		"role":         role,
		"executor":     executor,
		"priority":     priority,
		"attempt":      attempt,
		"max_attempts": maxAttempts,
		"reason":       reason,
	})
	if _, err := tx.ExecContext(ctx, `
INSERT INTO run_events (run_id, seq, level, event_type, message, payload)
VALUES ($1, 1, 'info', 'worker_queued', 'Worker run was queued', $2)`, run.ID, payloadBytes); err != nil {
		return domain.Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) DispatchQueuedRun() (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7108001002)`); err != nil {
		return domain.Run{}, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id::text, executor::text
FROM runs
WHERE status = 'queued'
ORDER BY
  CASE WHEN COALESCE(result->>'queue_priority', '') ~ '^-?[0-9]+$'
       THEN (result->>'queue_priority')::int
       ELSE 0
  END DESC,
  created_at ASC
LIMIT 50`)
	if err != nil {
		return domain.Run{}, err
	}
	defer rows.Close()

	type queuedRun struct {
		id       string
		executor string
	}
	queued := []queuedRun{}
	for rows.Next() {
		var item queuedRun
		if err := rows.Scan(&item.id, &item.executor); err != nil {
			return domain.Run{}, err
		}
		queued = append(queued, item)
	}
	if len(queued) == 0 {
		return domain.Run{}, ErrNotFound
	}

	for _, item := range queued {
		nodeID, err := s.selectExecutorNode(ctx, tx, item.executor)
		if errors.Is(err, ErrNoCapacity) {
			continue
		}
		if err != nil {
			return domain.Run{}, err
		}
		run, err := scanRun(tx.QueryRowContext(ctx, `
UPDATE runs
SET status = 'running',
    executor_node_id = $2,
    started_at = now()
WHERE id = $1 AND status = 'queued'
RETURNING id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at`,
			item.id, nullString(nodeID),
		))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return domain.Run{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = 'running', updated_at = now() WHERE id = $1`, run.TaskID); err != nil {
			return domain.Run{}, err
		}
		payloadBytes, _ := json.Marshal(map[string]any{
			"role":             run.Role,
			"executor":         run.Executor,
			"executor_node_id": nodeID,
			"priority":         intFromMap(run.Result, "queue_priority", 0),
			"attempt":          intFromMap(run.Result, "retry_attempt", 1),
			"max_attempts":     intFromMap(run.Result, "max_attempts", 1),
		})
		if _, err := tx.ExecContext(ctx, `
WITH next_seq AS (
  SELECT COALESCE(MAX(seq), 0) + 1 AS seq
  FROM run_events
  WHERE run_id = $1
)
INSERT INTO run_events (run_id, seq, level, event_type, message, payload)
SELECT $1, seq, 'info', 'worker_spawn', 'Queued worker run was dispatched', $2 FROM next_seq`, run.ID, payloadBytes); err != nil {
			return domain.Run{}, err
		}
		if err := tx.Commit(); err != nil {
			return domain.Run{}, err
		}
		return run, nil
	}
	return domain.Run{}, ErrNoCapacity
}

func (s *PostgresStore) selectExecutorNode(ctx context.Context, tx *sql.Tx, executor string) (string, error) {
	var nodeID string
	err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM (
  SELECT n.id,
         n.last_seen_at,
         n.created_at,
         COUNT(r.id) AS active_runs,
         GREATEST(
           CASE
             WHEN COALESCE(n.capacity->>'concurrency', '') ~ '^[0-9]+$'
             THEN (n.capacity->>'concurrency')::int
             ELSE 1
           END,
           1
         ) AS concurrency
  FROM executor_nodes n
  LEFT JOIN runs r ON r.executor_node_id = n.id
    AND r.status IN ('queued', 'preparing', 'running')
  WHERE n.kind = $1
    AND n.status = 'active'
    AND ($1 <> 'ssh' OR n.host_key_verified)
  GROUP BY n.id, n.capacity, n.last_seen_at, n.created_at
) candidates
WHERE active_runs < concurrency
ORDER BY (active_runs::numeric / concurrency::numeric) ASC,
         (concurrency - active_runs) DESC,
         last_seen_at DESC NULLS LAST,
         created_at ASC
LIMIT 1`, executor).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoCapacity
	}
	if err != nil {
		s.log.Error("select executor node failed", "error", err, "executor", executor)
		return "", err
	}
	return nodeID, nil
}

func (s *PostgresStore) GetRun(id string) (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	run, err := scanRun(s.db.QueryRowContext(ctx, `
SELECT id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at
FROM runs
WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, ErrNotFound
	}
	return run, err
}

func (s *PostgresStore) UpdateRunWorkspace(runID string, branch string, worktreePath string) (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	run, err := scanRun(s.db.QueryRowContext(ctx, `
UPDATE runs
SET branch = $2,
    worktree_path = $3
WHERE id = $1
RETURNING id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at`,
		runID, branch, worktreePath))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, ErrNotFound
	}
	return run, err
}

func (s *PostgresStore) ListAllRuns() []domain.Run {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at
FROM runs
ORDER BY created_at DESC
LIMIT 200`)
	if err != nil {
		s.log.Error("list all runs failed", "error", err)
		return nil
	}
	defer rows.Close()

	runs := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			s.log.Error("scan run failed", "error", err)
			return runs
		}
		runs = append(runs, run)
	}
	return runs
}

func (s *PostgresStore) ListRuns(taskID string) []domain.Run {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at
FROM runs
WHERE task_id = $1
ORDER BY created_at ASC`, taskID)
	if err != nil {
		s.log.Error("list runs failed", "error", err)
		return nil
	}
	defer rows.Close()

	runs := []domain.Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			s.log.Error("scan run failed", "error", err)
			return runs
		}
		runs = append(runs, run)
	}
	return runs
}

func (s *PostgresStore) FinishRun(runID string, status string, result map[string]any) (domain.Run, error) {
	ctx, cancel := storeContext()
	defer cancel()

	resultBytes, _ := json.Marshal(result)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Run{}, err
	}
	defer tx.Rollback()

	run, err := scanRun(tx.QueryRowContext(ctx, `
UPDATE runs
SET status = $2, result = $3, finished_at = now()
WHERE id = $1
RETURNING id::text, task_id::text, role::text, status::text, executor::text, COALESCE(executor_node_id::text, ''), branch, worktree_path, result, started_at, finished_at, created_at`,
		runID, status, resultBytes))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Run{}, ErrNotFound
	}
	if err != nil {
		return domain.Run{}, err
	}

	taskStatus := taskStatusForRun(run.Role, status)
	if taskStatus != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET status = $2, updated_at = now() WHERE id = $1`, run.TaskID, taskStatus); err != nil {
			return domain.Run{}, err
		}
	}

	payloadBytes, _ := json.Marshal(map[string]any{"status": status, "result": result})
	if _, err := tx.ExecContext(ctx, `
WITH next_seq AS (
  SELECT COALESCE(MAX(seq), 0) + 1 AS seq
  FROM run_events
  WHERE run_id = $1
)
INSERT INTO run_events (run_id, seq, level, event_type, message, payload)
SELECT $1, seq, 'info', 'worker_result', 'Worker result was recorded', $2 FROM next_seq`, runID, payloadBytes); err != nil {
		return domain.Run{}, err
	}

	if err := tx.Commit(); err != nil {
		return domain.Run{}, err
	}
	return run, nil
}

func (s *PostgresStore) ListEvents(runID string) []domain.RunEvent {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id, run_id::text, seq, level, event_type, message, payload, created_at
FROM run_events
WHERE run_id = $1
ORDER BY seq ASC`, runID)
	if err != nil {
		s.log.Error("list run events failed", "error", err)
		return nil
	}
	defer rows.Close()

	events := []domain.RunEvent{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			s.log.Error("scan run event failed", "error", err)
			return events
		}
		events = append(events, event)
	}
	return events
}

func (s *PostgresStore) AddEvent(runID string, level string, eventType string, message string, payload map[string]any) (domain.RunEvent, error) {
	ctx, cancel := storeContext()
	defer cancel()

	payloadBytes, _ := json.Marshal(payload)
	event, err := scanEvent(s.db.QueryRowContext(ctx, `
WITH next_seq AS (
  SELECT COALESCE(MAX(seq), 0) + 1 AS seq
  FROM run_events
  WHERE run_id = $1
)
INSERT INTO run_events (run_id, seq, level, event_type, message, payload)
SELECT $1, seq, $2, $3, $4, $5 FROM next_seq
RETURNING id, run_id::text, seq, level, event_type, message, payload, created_at`,
		runID, level, eventType, message, payloadBytes))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunEvent{}, ErrNotFound
	}
	return event, err
}

func (s *PostgresStore) RecordToolCall(call domain.ToolCall) domain.ToolCall {
	ctx, cancel := storeContext()
	defer cancel()

	inputBytes, _ := json.Marshal(call.Input)
	outputBytes, _ := json.Marshal(call.Output)
	var runID any
	if call.RunID != "" {
		runID = call.RunID
	}

	err := s.db.QueryRowContext(ctx, `
INSERT INTO tool_calls (run_id, caller, tool_name, input, output, status, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
RETURNING id::text, created_at, finished_at`,
		runID, call.Caller, call.ToolName, inputBytes, outputBytes, call.Status,
	).Scan(&call.ID, &call.CreatedAt, &call.FinishedAt)
	if err != nil {
		s.log.Error("record tool call failed", "error", err, "tool", call.ToolName)
	}
	if call.Input == nil {
		call.Input = map[string]any{}
	}
	if call.Output == nil {
		call.Output = map[string]any{}
	}
	return call
}

func (s *PostgresStore) ListAuditLogs() []domain.AuditLog {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, org_id::text, actor_type, actor_id, action, resource_type, resource_id, payload,
       COALESCE(prev_hash, ''), COALESCE(entry_hash, ''), created_at
FROM audit_logs
ORDER BY created_at DESC
LIMIT 200`)
	if err != nil {
		s.log.Error("list audit logs failed", "error", err)
		return nil
	}
	defer rows.Close()

	logs := []domain.AuditLog{}
	for rows.Next() {
		var entry domain.AuditLog
		var payloadBytes []byte
		if err := rows.Scan(&entry.ID, &entry.OrgID, &entry.ActorType, &entry.ActorID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &payloadBytes, &entry.PrevHash, &entry.EntryHash, &entry.CreatedAt); err != nil {
			s.log.Error("scan audit log failed", "error", err)
			return logs
		}
		entry.Payload = decodeMap(payloadBytes)
		logs = append(logs, entry)
	}
	return logs
}

func (s *PostgresStore) ListAuditLogsForSeal() []domain.AuditLog {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, org_id::text, actor_type, actor_id, action, resource_type, resource_id, payload,
       COALESCE(prev_hash, ''), COALESCE(entry_hash, ''), created_at
FROM audit_logs
ORDER BY created_at ASC, id ASC`)
	if err != nil {
		s.log.Error("list audit logs for seal failed", "error", err)
		return nil
	}
	defer rows.Close()

	logs := []domain.AuditLog{}
	for rows.Next() {
		var entry domain.AuditLog
		var payloadBytes []byte
		if err := rows.Scan(&entry.ID, &entry.OrgID, &entry.ActorType, &entry.ActorID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &payloadBytes, &entry.PrevHash, &entry.EntryHash, &entry.CreatedAt); err != nil {
			s.log.Error("scan audit log for seal failed", "error", err)
			return logs
		}
		entry.Payload = decodeMap(payloadBytes)
		logs = append(logs, entry)
	}
	return logs
}

func (s *PostgresStore) RecordAuditLog(entry domain.AuditLog) domain.AuditLog {
	ctx, cancel := storeContext()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.log.Error("begin audit log transaction failed", "error", err, "action", entry.Action)
		return entry
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7108001001)`); err != nil {
		s.log.Error("lock audit hash chain failed", "error", err, "action", entry.Action)
		return entry
	}

	if entry.OrgID == "" {
		entry.OrgID = defaultOrgID
	}
	var prevHash string
	err = tx.QueryRowContext(ctx, `
SELECT COALESCE(entry_hash, '')
FROM audit_logs
WHERE org_id = $1 AND COALESCE(entry_hash, '') <> ''
ORDER BY created_at DESC, id DESC
LIMIT 1`, entry.OrgID).Scan(&prevHash)
	if errors.Is(err, sql.ErrNoRows) {
		prevHash = ""
	} else if err != nil {
		s.log.Error("load previous audit hash failed", "error", err)
	}
	entry = prepareAuditEntry(entry, prevHash)
	payloadBytes, _ := json.Marshal(entry.Payload)
	err = tx.QueryRowContext(ctx, `
INSERT INTO audit_logs (org_id, actor_type, actor_id, action, resource_type, resource_id, payload, prev_hash, entry_hash, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id::text`,
		entry.OrgID, entry.ActorType, entry.ActorID, entry.Action, entry.ResourceType, entry.ResourceID, payloadBytes, entry.PrevHash, entry.EntryHash, entry.CreatedAt,
	).Scan(&entry.ID)
	if err != nil {
		s.log.Error("record audit log failed", "error", err, "action", entry.Action)
		return entry
	}
	if err := tx.Commit(); err != nil {
		s.log.Error("commit audit log failed", "error", err, "action", entry.Action)
		return entry
	}
	if err := exportAuditEntry(entry); err != nil {
		s.log.Error("export audit log failed", "error", err, "action", entry.Action)
	}
	return entry
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) scanTaskRow(row rowScanner) (domain.Task, error) {
	return scanTask(row)
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var envelopeBytes []byte
	err := row.Scan(&task.ID, &task.ProjectID, &task.RepositoryID, &task.TaskKey, &task.Title, &task.Status, &envelopeBytes, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return domain.Task{}, err
	}
	task.Envelope = decodeEnvelope(envelopeBytes)
	return task, nil
}

func scanRun(row rowScanner) (domain.Run, error) {
	var run domain.Run
	var branch sql.NullString
	var worktreePath sql.NullString
	var resultBytes []byte
	err := row.Scan(&run.ID, &run.TaskID, &run.Role, &run.Status, &run.Executor, &run.ExecutorNodeID, &branch, &worktreePath, &resultBytes, &run.StartedAt, &run.FinishedAt, &run.CreatedAt)
	if err != nil {
		return domain.Run{}, err
	}
	run.Branch = branch.String
	run.WorktreePath = worktreePath.String
	run.Result = decodeMap(resultBytes)
	return run, nil
}

func scanEvent(row rowScanner) (domain.RunEvent, error) {
	var event domain.RunEvent
	var payloadBytes []byte
	err := row.Scan(&event.ID, &event.RunID, &event.Seq, &event.Level, &event.EventType, &event.Message, &payloadBytes, &event.CreatedAt)
	if err != nil {
		return domain.RunEvent{}, err
	}
	event.Payload = decodeMap(payloadBytes)
	return event, nil
}

func decodeEnvelope(data []byte) domain.TaskEnvelope {
	var envelope domain.TaskEnvelope
	_ = json.Unmarshal(data, &envelope)
	return envelope
}

func decodeMap(data []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func dbRole(role string) string {
	if role == "git-sync" {
		return "git_sync"
	}
	return role
}

func taskStatusForRun(role string, runStatus string) string {
	switch runStatus {
	case "succeeded":
		if role == "git_sync" || role == "git-sync" {
			return "completed"
		}
		return "running"
	case "blocked":
		return "blocked"
	case "failed", "timed_out", "cancelled":
		return "failed"
	default:
		return ""
	}
}

func storeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
