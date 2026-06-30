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
	cookie := localSessionCookie(t, server.Handler())

	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID, nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID+"/repositories", nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+foreignProject.ID+"/agent-profiles", nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/tasks/"+foreignTask.ID, nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/tasks/"+foreignTask.ID+"/runs", nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/runs/"+foreignRun.ID, nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/runs/"+foreignRun.ID+"/artifacts", nil, cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/artifacts/"+foreignArtifact.ID+"/content", nil, cookie)
	assertForbidden(t, server, http.MethodPost, "/api/v1/approvals/"+foreignApproval.ID+"/decision", []byte(`{"status":"approved"}`), cookie)
	assertForbidden(t, server, http.MethodPost, "/api/v1/executor-nodes/"+foreignNode.ID+"/verify-host-key", []byte(`{"observed_fingerprint":"SHA256:test"}`), cookie)
	assertForbidden(t, server, http.MethodGet, "/api/v1/skills/"+foreignSkill.ID+"/versions", nil, cookie)

	assertListDoesNotContain(t, server, "/api/v1/projects", foreignProject.ID, cookie)
	assertListDoesNotContain(t, server, "/api/v1/approvals", foreignApproval.ID, cookie)
	assertListDoesNotContain(t, server, "/api/v1/executor-nodes", foreignNode.ID, cookie)
	assertListDoesNotContain(t, server, "/api/v1/skills", foreignSkill.ID, cookie)
	assertListDoesNotContain(t, server, "/api/v1/audit-logs", foreignAudit.ID, cookie)

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

func TestAPIProjectMembershipScopesProjectAccess(t *testing.T) {
	st := store.NewMemoryStore()
	member, err := st.UpsertUser(domain.User{Email: "dev@example.com", DisplayName: "Dev User"}, "org_default", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	allowedProject := st.CreateProject(domain.Project{OrgID: "org_default", Name: "Allowed Project", Slug: "allowed-project"})
	allowedRepo := st.CreateRepository(domain.Repository{ProjectID: allowedProject.ID, Name: "repo", Provider: "local", RemoteURL: "file:///allowed.git"})
	allowedTask := st.CreateTask(domain.TaskEnvelope{
		TaskID:         "ALLOWED-1",
		ProjectID:      allowedProject.ID,
		RepositoryID:   allowedRepo.ID,
		Title:          "Allowed task",
		Role:           "feature",
		Skill:          "company-feature-worker",
		AgentProfile:   "feature-worker-go-node",
		Executor:       "docker",
		AllowedPaths:   []string{"internal/**"},
		ForbiddenPaths: []string{".env*"},
	})
	deniedProject := st.CreateProject(domain.Project{OrgID: "org_default", Name: "Denied Project", Slug: "denied-project"})
	if _, err := st.UpsertProjectMembership(domain.ProjectMembership{ProjectID: allowedProject.ID, UserID: member.User.ID, Role: "developer"}); err != nil {
		t.Fatal(err)
	}
	memberAuth, err := st.UpsertUser(domain.User{Email: "dev@example.com", DisplayName: "Dev User"}, "org_default", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AuthMode: "local"}
	server := NewServer(cfg, authOverrideStore{Store: st, auth: memberAuth}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cookie := localSessionCookie(t, server.Handler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+allowedProject.ID+"/tasks", nil)
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("allowed project status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), allowedTask.ID) {
		t.Fatalf("allowed task missing: %s", resp.Body.String())
	}

	assertForbidden(t, server, http.MethodGet, "/api/v1/projects/"+deniedProject.ID, nil, cookie)
	assertListDoesNotContain(t, server, "/api/v1/projects", deniedProject.ID, cookie)
}

func TestAPIProjectAdminCannotCreateOrganizationProject(t *testing.T) {
	st := store.NewMemoryStore()
	member, err := st.UpsertUser(domain.User{Email: "project-admin@example.com", DisplayName: "Project Admin"}, "org_default", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	project := st.CreateProject(domain.Project{OrgID: "org_default", Name: "Scoped Project", Slug: "scoped-project"})
	if _, err := st.UpsertProjectMembership(domain.ProjectMembership{ProjectID: project.ID, UserID: member.User.ID, Role: "project_admin"}); err != nil {
		t.Fatal(err)
	}
	memberAuth, err := st.UpsertUser(domain.User{Email: "project-admin@example.com", DisplayName: "Project Admin"}, "org_default", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{AuthMode: "local"}, authOverrideStore{Store: st, auth: memberAuth}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cookie := localSessionCookie(t, server.Handler())

	assertForbidden(t, server, http.MethodPost, "/api/v1/projects", []byte(`{"name":"Should Not Exist","slug":"should-not-exist"}`), cookie)
}

func TestAdminUsersAndProjectMembersAPI(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{AuthMode: "local"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var auth domain.AuthContext
	apiDo(t, server.Handler(), http.MethodPost, "/api/v1/users", map[string]any{
		"email":        "admin-target@example.com",
		"display_name": "Admin Target",
		"role":         "viewer",
	}, http.StatusCreated, &auth)
	if auth.User.ID == "" || auth.Membership.Role != "viewer" {
		t.Fatalf("created auth = %#v", auth)
	}

	project := st.CreateProject(domain.Project{OrgID: "org_default", Name: "Enterprise Project", Slug: "enterprise-project"})
	var membership domain.ProjectMembership
	apiDo(t, server.Handler(), http.MethodPost, "/api/v1/projects/"+project.ID+"/members", map[string]any{
		"user_id": auth.User.ID,
		"role":    "maintainer",
	}, http.StatusCreated, &membership)
	if membership.ProjectID != project.ID || membership.UserID != auth.User.ID || membership.Role != "maintainer" {
		t.Fatalf("membership = %#v", membership)
	}
}

type authOverrideStore struct {
	store.Store
	auth domain.AuthContext
}

func (s authOverrideStore) GetAuthContext() domain.AuthContext {
	return s.auth
}

func (s authOverrideStore) GetUserByEmail(email string) (domain.AuthContext, string, error) {
	_, passwordHash, err := s.Store.GetUserByEmail(email)
	if err != nil {
		return domain.AuthContext{}, "", err
	}
	return s.auth, passwordHash, nil
}

func assertForbidden(t *testing.T, server *Server, method string, path string, body []byte, cookie *http.Cookie) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("%s %s status = %d, body = %s", method, path, resp.Code, resp.Body.String())
	}
}

func assertListDoesNotContain(t *testing.T, server *Server, path string, forbiddenID string, cookie *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %s", path, resp.Code, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), forbiddenID) {
		t.Fatalf("GET %s leaked forbidden id %s: %s", path, forbiddenID, resp.Body.String())
	}
}
