package api

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestAPIResourceAuthorizationDeniesCrossOrganizationResources(t *testing.T) {
	st := store.NewMemoryStore()
	foreignOrg, err := st.CreateOrganization(domain.Organization{Name: "Other Org", Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	foreignProject := st.CreateProject(domain.Project{OrgID: foreignOrg.ID, Name: "Other Project", Slug: "other-project"})
	foreignRepo := st.CreateRepository(domain.Repository{ProjectID: foreignProject.ID, Name: "repo", Provider: "local", RemoteURL: "file:///repo.git"})
	foreignTask := st.CreateTask(domain.TaskEnvelope{
		TaskID:       "FOREIGN-1",
		ProjectID:    foreignProject.ID,
		RepositoryID: foreignRepo.ID,
		Title:        "Foreign task",
		Role:         "feature",
		Skill:        "company-feature-worker",
		AgentProfile: "feature-worker-go-node",
		Executor:     "docker",
	})
	foreignRun, err := st.StartRun(foreignTask.ID, "feature", "docker")
	if err != nil {
		t.Fatal(err)
	}
	foreignArtifact, err := st.CreateArtifact(domain.Artifact{RunID: foreignRun.ID, Kind: "result", Name: "result.json", Path: "memory://foreign", Metadata: map[string]any{"content": "{}"}})
	if err != nil {
		t.Fatal(err)
	}
	foreignApproval, err := st.CreateApproval(domain.Approval{TaskID: foreignTask.ID, ApprovalType: "pr_prepare", Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	foreignNode, err := st.RegisterExecutorNode(domain.ExecutorNode{OrgID: foreignOrg.ID, Kind: "docker", Name: "foreign-node", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	foreignSkill, err := st.CreateSkill(domain.Skill{OrgID: foreignOrg.ID, Name: "foreign-skill", Role: "feature", Enabled: true}, domain.SkillVersion{Version: "v1", ContentHash: "hash", Path: "skills/foreign/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	foreignAudit := st.RecordAuditLog(domain.AuditLog{OrgID: foreignOrg.ID, ActorType: "human", ActorID: "foreign", Action: "foreign.action", ResourceType: "project", ResourceID: foreignProject.ID})

	server := NewServer(config.Config{AuthMode: "local"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID, nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID+"/repositories", nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID+"/agent-profiles", nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/tasks/"+foreignTask.ID, nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/tasks/"+foreignTask.ID+"/runs", nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/runs/"+foreignRun.ID, nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/runs/"+foreignRun.ID+"/artifacts", nil)
	assertForbidden(t, server, http.MethodGet, "/api/v1/artifacts/"+foreignArtifact.ID+"/content", nil)
	assertForbidden(t, server, http.MethodPost, "/api/v1/approvals/"+foreignApproval.ID+"/decision", []byte(`{"status":"approved"}`))
	assertForbidden(t, server, http.MethodPost, "/api/v1/executor-nodes/"+foreignNode.ID+"/verify-host-key", []byte(`{"observed_fingerprint":"SHA256:test"}`))
	assertForbidden(t, server, http.MethodGet, "/api/v1/skills/"+foreignSkill.ID+"/versions", nil)

	assertListDoesNotContain(t, server, "/api/v1/projects", foreignProject.ID)
	assertListDoesNotContain(t, server, "/api/v1/approvals", foreignApproval.ID)
	assertListDoesNotContain(t, server, "/api/v1/executor-nodes", foreignNode.ID)
	assertListDoesNotContain(t, server, "/api/v1/skills", foreignSkill.ID)
	assertListDoesNotContain(t, server, "/api/v1/audit-logs", foreignAudit.ID)

	var denied bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.authorization_denied" && entry.ResourceID == foreignProject.ID {
			if entry.Payload["trace_id"] == "" {
				t.Fatalf("authorization denial missing trace_id: %#v", entry.Payload)
			}
			denied = true
		}
	}
	if !denied {
		t.Fatalf("expected api.authorization_denied audit row for cross-org project")
	}
}

func TestAPIViewerCannotMutateResources(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := map[string]any{"keys": []map[string]any{apiTestJWK("test-key", &key.PublicKey)}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()
	token := apiSignTestToken(t, key, map[string]any{
		"sub":   "viewer-subject",
		"iss":   "https://issuer.example",
		"aud":   []string{"multi-codex"},
		"email": "viewer@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:         "oidc",
		OIDCIssuer:       "https://issuer.example",
		OIDCAudience:     "multi-codex",
		OIDCJWKSURL:      jwksServer.URL,
		OIDCDefaultRole:  "viewer",
		OIDCDefaultOrgID: "org_default",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{"name":"blocked","slug":"blocked"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("viewer mutation status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "permission denied") {
		t.Fatalf("viewer mutation body = %s", resp.Body.String())
	}
}

func assertForbidden(t *testing.T, server *Server, method string, path string, body []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("%s %s status = %d, body = %s", method, path, resp.Code, resp.Body.String())
	}
}

func assertListDoesNotContain(t *testing.T, server *Server, path string, forbiddenID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %s", path, resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), forbiddenID) {
		t.Fatalf("GET %s leaked forbidden id %s: %s", path, forbiddenID, resp.Body.String())
	}
}
