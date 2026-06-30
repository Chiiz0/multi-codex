package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
	"github.com/Chiiz0/multi-codex/internal/workflow"
)

func TestGitPublishPRAuditsCredentialMetadata(t *testing.T) {
	st := store.NewMemoryStore()
	task := apiGitPublishReadyTask(t, st)
	server := NewServer(config.Config{GitSyncMode: "dry-run"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cookie := localSessionCookie(t, server.Handler())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/workflow/git_publish_pr", nil)
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Run    domain.Run     `json:"run"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	if payload.Run.Status != "succeeded" {
		t.Fatalf("run = %#v", payload.Run)
	}

	assertAPIGitPublishEvent(t, st, payload.Run.ID, false)
	assertAPIGitPublishAudit(t, st, payload.Run.ID, false)
}

func TestGitPublishPRLiveCreatesProviderPRWithoutAutoMerge(t *testing.T) {
	st := store.NewMemoryStore()
	task := apiGitPublishReadyTask(t, st)
	var gotAuthorization string
	var gotPayload map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo/pulls" {
			t.Fatalf("provider path = %q", r.URL.Path)
		}
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode provider payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/example/repo/pull/42","number":42}`))
	}))
	defer provider.Close()
	server := NewServer(config.Config{
		GitSyncMode:  "live",
		GitHubAPIURL: provider.URL,
		GitHubToken:  "gh-live-token",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cookie := localSessionCookie(t, server.Handler())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+task.ID+"/workflow/git_publish_pr", nil)
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("publish status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Run    domain.Run     `json:"run"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	if payload.Run.Status != "succeeded" || payload.Result["status"] != "published" {
		t.Fatalf("publish payload = %#v", payload)
	}
	if payload.Result["dry_run"] != false || payload.Result["auto_merge"] != false {
		t.Fatalf("publish safety flags = %#v", payload.Result)
	}
	if payload.Result["pr_url"] != "https://github.com/example/repo/pull/42" {
		t.Fatalf("pr url = %#v", payload.Result["pr_url"])
	}
	if gotAuthorization != "token gh-live-token" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if gotPayload["head"] != "codex/publish-pr-metadata" || gotPayload["base"] != "main" {
		t.Fatalf("provider payload = %#v", gotPayload)
	}

	assertAPIGitPublishEvent(t, st, payload.Run.ID, true)
	assertAPIGitPublishAudit(t, st, payload.Run.ID, true)
}

func assertAPIGitPublishEvent(t *testing.T, st store.Store, runID string, wantCredentialResolved bool) {
	t.Helper()
	for _, event := range st.ListEvents(runID) {
		if event.EventType != "git_publish_pr" {
			continue
		}
		if event.Payload["credential_provider"] != "env" {
			t.Fatalf("credential provider event payload = %#v", event.Payload)
		}
		if resolved, ok := event.Payload["credential_resolved"].(bool); !ok || resolved != wantCredentialResolved {
			t.Fatalf("credential resolved event payload = %#v", event.Payload)
		}
		if event.Payload["auto_merge"] != false {
			t.Fatalf("auto_merge event payload = %#v", event.Payload)
		}
		return
	}
	t.Fatalf("expected git_publish_pr run event")
}

func assertAPIGitPublishAudit(t *testing.T, st store.Store, runID string, wantCredentialResolved bool) {
	t.Helper()
	for _, entry := range st.ListAuditLogs() {
		if entry.Action != "api.git_publish_pr" || entry.ResourceID != runID {
			continue
		}
		if entry.Payload["credential_provider"] != "env" {
			t.Fatalf("credential provider audit payload = %#v", entry.Payload)
		}
		if resolved, ok := entry.Payload["credential_resolved"].(bool); !ok || resolved != wantCredentialResolved {
			t.Fatalf("credential resolved audit payload = %#v", entry.Payload)
		}
		if entry.Payload["auto_merge"] != false {
			t.Fatalf("auto_merge audit payload = %#v", entry.Payload)
		}
		return
	}
	t.Fatalf("expected api.git_publish_pr audit row")
}

func apiGitPublishReadyTask(t *testing.T, st *store.MemoryStore) domain.Task {
	t.Helper()
	repo := st.CreateRepository(domain.Repository{
		ProjectID:     "proj_demo",
		Name:          "github-repo",
		Provider:      "github",
		RemoteURL:     "https://github.com/example/repo.git",
		DefaultBranch: "main",
	})
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          domain.NewID("API-GIT-PUBLISH"),
		ProjectID:       "proj_demo",
		RepositoryID:    repo.ID,
		Title:           "Publish PR metadata",
		BaseBranch:      "main",
		TargetBranch:    "codex/publish-pr-metadata",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
		Policy: domain.TaskPolicy{
			RequireAudit:         true,
			RequireTests:         true,
			RequireHumanBeforePR: true,
		},
	})
	feature, _ := st.StartRun(task.ID, "feature", "docker")
	_, _ = st.FinishRun(feature.ID, "succeeded", map[string]any{"status": "done"})
	_, _ = st.RecordScopeCheck(task.ID, feature.ID, "main", domain.ScopeCheckResult{Status: "passed", ChangedFiles: []string{"internal/api/router.go"}})
	testRun, _ := st.StartRun(task.ID, "test", "docker")
	_, _ = st.FinishRun(testRun.ID, "succeeded", map[string]any{"status": "done"})
	auditRun, _ := st.StartRun(task.ID, "audit", "docker")
	_, _ = st.FinishRun(auditRun.ID, "succeeded", map[string]any{"status": "done"})
	prepareApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_prepare", Status: "pending"})
	_, _ = st.DecideApproval(prepareApproval.ID, "approved", "reviewer", "ok")
	bodyArtifactID := domain.NewID("artifact")
	prepareRun, _ := st.StartRun(task.ID, "git_sync", "docker")
	_, _ = st.FinishRun(prepareRun.ID, "succeeded", map[string]any{
		"status":          "prepared",
		"pr_body":         "PR body",
		"pr_publish_plan": workflow.RenderPRPublishPlan(task, repo, bodyArtifactID),
	})
	publishApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_publish", Status: "pending"})
	_, _ = st.DecideApproval(publishApproval.ID, "approved", "reviewer", "ok")
	return task
}
