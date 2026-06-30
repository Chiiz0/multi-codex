package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestPilotWorkflowDryRunThenLivePRThroughAPI(t *testing.T) {
	st := store.NewMemoryStore()
	cfg := config.Config{
		ArtifactRoot:         filepath.Join(t.TempDir(), "artifacts"),
		RunRoot:              filepath.Join(t.TempDir(), "runs"),
		WorktreeRoot:         filepath.Join(t.TempDir(), "worktrees"),
		RepoCacheRoot:        filepath.Join(t.TempDir(), "repos"),
		ExecutorMode:         "mock",
		WorkerDefaultTimeout: 5 * time.Second,
		GitSyncMode:          "dry-run",
	}
	server := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := server.Handler()

	var org domain.Organization
	apiDo(t, handler, http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Pilot Team",
		"slug": "pilot-team",
	}, http.StatusCreated, &org)

	var project domain.Project
	apiDo(t, handler, http.MethodPost, "/api/v1/projects", map[string]any{
		"name":        "Pilot Project",
		"slug":        "pilot-project",
		"description": "Low-risk repository pilot.",
	}, http.StatusCreated, &project)

	for _, skill := range []struct {
		name string
		role string
	}{
		{"pilot-feature-worker", "feature"},
		{"pilot-test-worker", "test"},
		{"pilot-audit-worker", "audit"},
		{"pilot-git-sync", "git_sync"},
	} {
		var created domain.Skill
		apiDo(t, handler, http.MethodPost, "/api/v1/skills", map[string]any{
			"name":         skill.name,
			"role":         skill.role,
			"description":  "Pilot " + skill.role + " skill.",
			"version":      "pilot-v1",
			"content_hash": "hash-" + skill.name,
			"path":         "skills/" + skill.name + "/SKILL.md",
		}, http.StatusCreated, &created)
	}

	for _, profile := range []domain.AgentProfile{
		pilotProfile(project.ID, "pilot-feature-profile", "feature", "workspace-write"),
		pilotProfile(project.ID, "pilot-test-profile", "test", "workspace-write"),
		pilotProfile(project.ID, "pilot-audit-profile", "audit", "read-only"),
		pilotProfile(project.ID, "pilot-git-sync-profile", "git_sync", "workspace-write"),
	} {
		var created domain.AgentProfile
		apiDo(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/agent-profiles", profile, http.StatusCreated, &created)
	}

	var node domain.ExecutorNode
	apiDo(t, handler, http.MethodPost, "/api/v1/executor-nodes", domain.ExecutorNode{
		Kind:            "docker",
		Name:            "pilot-docker",
		Status:          "active",
		HostKeyVerified: true,
		Capacity:        map[string]any{"concurrency": 1},
		Labels:          map[string]any{"pilot": true},
	}, http.StatusCreated, &node)

	var repo domain.Repository
	apiDo(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/repositories", domain.Repository{
		Name:            "pilot-repo",
		Provider:        "github",
		RemoteURL:       "https://github.com/example/repo.git",
		DefaultBranch:   "main",
		LocalMirrorPath: createPilotMirror(t),
	}, http.StatusCreated, &repo)

	var createdTask struct {
		Task       domain.Task             `json:"task"`
		Validation domain.ValidationResult `json:"validation"`
	}
	envelope := domain.TaskEnvelope{
		TaskID:          domain.NewID("PILOT"),
		ProjectID:       project.ID,
		RepositoryID:    repo.ID,
		Title:           "Pilot governed PR workflow",
		BaseBranch:      "main",
		TargetBranch:    "codex/pilot-governed-pr",
		Role:            "feature",
		Skill:           "pilot-feature-worker",
		AgentProfile:    "pilot-feature-profile",
		Executor:        "docker",
		AllowedPaths:    []string{"README.md", "docs/**", "internal/**"},
		ForbiddenPaths:  []string{".env*", "secrets/**", ".github/**", "infra/**", "terraform/**", "go.sum", "pnpm-lock.yaml"},
		AllowedCommands: []string{"go test ./..."},
		Network:         false,
		Objective:       "Exercise dry-run PR publishing before live PR creation.",
		AcceptanceCriteria: []string{
			"Feature, scope, test, audit, approval, and Git Sync gates are visible.",
			"Dry-run publish happens before live provider PR creation.",
		},
		StopConditions:  []string{"scope violation", "test failure", "audit blocker"},
		RequiredOutputs: []string{"summary", "changed_files", "tests_run", "risks", "needs_human"},
		Policy: domain.TaskPolicy{
			AllowPush:             false,
			AllowDependencyChange: false,
			AllowInfraChange:      false,
			RequireAudit:          true,
			RequireTests:          true,
			RequireHumanBeforePR:  true,
		},
	}
	apiDo(t, handler, http.MethodPost, "/api/v1/projects/"+project.ID+"/tasks", map[string]any{"envelope": envelope}, http.StatusCreated, &createdTask)
	if !createdTask.Validation.Valid {
		t.Fatalf("task validation = %#v", createdTask.Validation)
	}
	task := createdTask.Task

	var validation domain.ValidationResult
	apiDo(t, handler, http.MethodPost, "/api/v1/tasks/"+task.ID+"/validate", map[string]any{}, http.StatusOK, &validation)
	if !validation.Valid {
		t.Fatalf("validate task = %#v", validation)
	}

	featureRun := startPilotRun(t, handler, st, "/api/v1/tasks/"+task.ID+"/start")
	apiDo(t, handler, http.MethodPost, "/api/v1/tasks/"+task.ID+"/scope-check", map[string]any{
		"changed_files": []string{"README.md"},
	}, http.StatusOK, &map[string]any{})
	testRun := startPilotRun(t, handler, st, "/api/v1/tasks/"+task.ID+"/workflow/test_run_required")
	auditRun := startPilotRun(t, handler, st, "/api/v1/tasks/"+task.ID+"/workflow/audit_run")
	if featureRun.Role != "feature" || testRun.Role != "test" || auditRun.Role != "audit" {
		t.Fatalf("pilot run roles = %s/%s/%s", featureRun.Role, testRun.Role, auditRun.Role)
	}

	prepareApproval := requestPilotApproval(t, handler, task.ID, "approval_request")
	decidePilotApproval(t, handler, prepareApproval.ID)

	var prepared struct {
		Run      domain.Run      `json:"run"`
		Result   map[string]any  `json:"result"`
		Artifact domain.Artifact `json:"artifact"`
	}
	apiDo(t, handler, http.MethodPost, "/api/v1/tasks/"+task.ID+"/workflow/git_prepare_pr", map[string]any{}, http.StatusCreated, &prepared)
	assertPilotPlan(t, prepared.Result)

	publishApproval := requestPilotApproval(t, handler, task.ID, "approval_request_pr_publish")
	decidePilotApproval(t, handler, publishApproval.ID)

	var dryRun struct {
		Run    domain.Run     `json:"run"`
		Result map[string]any `json:"result"`
	}
	apiDo(t, handler, http.MethodPost, "/api/v1/tasks/"+task.ID+"/workflow/git_publish_pr", map[string]any{}, http.StatusCreated, &dryRun)
	if dryRun.Result["status"] != "publish_prepared" || dryRun.Result["dry_run"] != true || dryRun.Result["auto_merge"] != false {
		t.Fatalf("dry-run publish result = %#v", dryRun.Result)
	}

	var gotProviderPayload map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo/pulls" {
			t.Fatalf("provider path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token gh-pilot-token" {
			t.Fatalf("provider authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotProviderPayload); err != nil {
			t.Fatalf("decode provider payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/example/repo/pull/77","number":77}`))
	}))
	defer provider.Close()
	liveServer := NewServer(config.Config{
		GitSyncMode:          "live",
		GitSyncLiveReviewed:  true,
		GitHubAPIURL:         provider.URL,
		GitHubToken:          "gh-pilot-token",
		WorkerDefaultTimeout: 5 * time.Second,
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var live struct {
		Run    domain.Run     `json:"run"`
		Result map[string]any `json:"result"`
	}
	apiDo(t, liveServer.Handler(), http.MethodPost, "/api/v1/tasks/"+task.ID+"/workflow/git_publish_pr", map[string]any{}, http.StatusCreated, &live)
	if live.Result["status"] != "published" || live.Result["dry_run"] != false || live.Result["auto_merge"] != false {
		t.Fatalf("live publish result = %#v", live.Result)
	}
	if live.Result["pr_url"] != "https://github.com/example/repo/pull/77" {
		t.Fatalf("live PR URL = %#v", live.Result["pr_url"])
	}
	if gotProviderPayload["head"] != "codex/pilot-governed-pr" || gotProviderPayload["base"] != "main" {
		t.Fatalf("provider payload = %#v", gotProviderPayload)
	}
	assertPilotAuditEvidence(t, st, dryRun.Run.ID, live.Run.ID)
}

func pilotProfile(projectID string, name string, role string, sandbox string) domain.AgentProfile {
	return domain.AgentProfile{
		ProjectID:      projectID,
		Name:           name,
		Role:           role,
		Model:          "gpt-5",
		SandboxMode:    sandbox,
		ApprovalPolicy: "never",
		Executor:       "docker",
		Image:          "multi-codex/codex-worker:go1.25-node-vite8",
		NetworkEnabled: false,
		Config:         map[string]any{"pilot": true},
	}
}

func startPilotRun(t *testing.T, handler http.Handler, st *store.MemoryStore, path string) domain.Run {
	t.Helper()
	var payload struct {
		Run domain.Run `json:"run"`
	}
	apiDo(t, handler, http.MethodPost, path, map[string]any{}, http.StatusAccepted, &payload)
	return waitPilotRun(t, st, payload.Run.ID)
}

func waitPilotRun(t *testing.T, st *store.MemoryStore, runID string) domain.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err == nil && run.Status == "succeeded" {
			return run
		}
		if err == nil && (run.Status == "failed" || run.Status == "blocked" || run.Status == "timed_out") {
			t.Fatalf("run %s ended with %s: %#v", runID, run.Status, run.Result)
		}
		time.Sleep(25 * time.Millisecond)
	}
	run, _ := st.GetRun(runID)
	t.Fatalf("run %s did not succeed before timeout: %#v", runID, run)
	return domain.Run{}
}

func requestPilotApproval(t *testing.T, handler http.Handler, taskID string, action string) domain.Approval {
	t.Helper()
	var approval domain.Approval
	apiDo(t, handler, http.MethodPost, "/api/v1/tasks/"+taskID+"/workflow/"+action, map[string]any{}, http.StatusCreated, &approval)
	if approval.Status != "pending" {
		t.Fatalf("approval = %#v", approval)
	}
	return approval
}

func decidePilotApproval(t *testing.T, handler http.Handler, approvalID string) {
	t.Helper()
	var approval domain.Approval
	apiDo(t, handler, http.MethodPost, "/api/v1/approvals/"+approvalID+"/decision", map[string]any{
		"status": "approved",
		"reason": "Pilot evidence reviewed.",
	}, http.StatusOK, &approval)
	if approval.Status != "approved" {
		t.Fatalf("approval decision = %#v", approval)
	}
}

func assertPilotPlan(t *testing.T, result map[string]any) {
	t.Helper()
	if result["status"] != "prepared" || result["auto_merge"] != false || result["allow_push"] != false {
		t.Fatalf("prepare result = %#v", result)
	}
	plan, ok := result["pr_publish_plan"].(map[string]any)
	if !ok {
		t.Fatalf("missing publish plan: %#v", result)
	}
	if plan["required_approval"] != "pr_publish" || plan["auto_merge"] != false || plan["provider"] != "github" {
		t.Fatalf("publish plan = %#v", plan)
	}
}

func assertPilotAuditEvidence(t *testing.T, st *store.MemoryStore, dryRunID string, liveRunID string) {
	t.Helper()
	var sawTaskCreate, sawPrepare, sawDry, sawLive bool
	for _, entry := range st.ListAuditLogs() {
		switch {
		case entry.Action == "api.task_create":
			sawTaskCreate = true
		case entry.Action == "api.git_prepare_pr":
			sawPrepare = true
		case entry.Action == "api.git_publish_pr" && entry.ResourceID == dryRunID:
			sawDry = entry.Payload["dry_run"] == true && entry.Payload["auto_merge"] == false
		case entry.Action == "api.git_publish_pr" && entry.ResourceID == liveRunID:
			sawLive = entry.Payload["dry_run"] == false && entry.Payload["credential_resolved"] == true && entry.Payload["auto_merge"] == false
		}
	}
	if !sawTaskCreate || !sawPrepare || !sawDry || !sawLive {
		t.Fatalf("missing pilot audit evidence task=%v prepare=%v dry=%v live=%v", sawTaskCreate, sawPrepare, sawDry, sawLive)
	}
}

func apiDo(t *testing.T, handler http.Handler, method string, path string, body any, wantStatus int, out any) {
	t.Helper()
	cookie := localSessionCookie(t, handler)
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, resp.Code, wantStatus, resp.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s %s: %v; body = %s", method, path, err, resp.Body.String())
		}
	}
}

func localSessionCookie(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	body := bytes.NewReader([]byte(`{"email":"local-dev@multi-codex.invalid","password":"admin123"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		return nil
	}
	for _, cookie := range resp.Result().Cookies() {
		if cookie.Name == authSessionCookieName {
			return cookie
		}
	}
	return nil
}

func createPilotMirror(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	mirror := filepath.Join(root, "repo.git")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{
		{"init"},
		{"config", "user.email", "pilot@example.invalid"},
		{"config", "user.name", "multi-codex pilot"},
	} {
		runPilotGit(t, ctx, source, command...)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("# Pilot Repository\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runPilotGit(t, ctx, source, "add", ".")
	runPilotGit(t, ctx, source, "commit", "-m", "seed pilot repo")
	runPilotGit(t, ctx, source, "branch", "-M", "main")
	cmd := exec.CommandContext(ctx, "git", "clone", "--bare", source, mirror)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare failed: %v: %s", err, string(output))
	}
	return mirror
}

func runPilotGit(t *testing.T, ctx context.Context, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, string(output))
	}
}
