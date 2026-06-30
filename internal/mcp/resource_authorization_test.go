package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestMCPResourceAuthorizationDeniesCrossOrganizationTools(t *testing.T) {
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
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, status := server.invokeTool(context.Background(), "task_list", mustRaw(t, map[string]any{"project_id": foreignProject.ID}))
	if status != http.StatusForbidden {
		t.Fatalf("task_list status = %d", status)
	}
	_, status = server.invokeTool(context.Background(), "task_get", mustRaw(t, map[string]any{"task_id": foreignTask.ID}))
	if status != http.StatusForbidden {
		t.Fatalf("task_get status = %d", status)
	}

	output, status := server.invokeTool(context.Background(), "organization_list", mustRaw(t, map[string]any{}))
	if status != http.StatusOK {
		t.Fatalf("organization_list status = %d", status)
	}
	data, _ := json.Marshal(output)
	if string(data) == "" || containsJSON(data, foreignOrg.ID) {
		t.Fatalf("organization_list leaked foreign org: %s", string(data))
	}

	var denied bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "mcp.authorization_denied" && entry.ResourceID == foreignTask.ID {
			denied = true
			break
		}
	}
	if !denied {
		t.Fatalf("expected mcp.authorization_denied audit row")
	}
}

func TestMCPViewerCannotMutateResources(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	auth := domain.AuthContext{
		User: domain.User{ID: "viewer"},
		Membership: domain.Membership{
			OrgID: "org_default",
			Role:  "viewer",
		},
		Permissions: []string{"organizations:read", "projects:read", "tasks:read", "runs:read"},
	}
	ctx := context.WithValue(context.Background(), mcpAuthContextKey{}, auth)
	ctx = context.WithValue(ctx, actorContextKey{}, auth.User.ID)

	_, status := server.invokeTool(ctx, "task_create", mustRaw(t, map[string]any{"task_envelope": map[string]any{}}))
	if status != http.StatusForbidden {
		t.Fatalf("viewer task_create status = %d", status)
	}
}

func mustRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func containsJSON(data []byte, value string) bool {
	return json.Valid(data) && value != "" && strings.Contains(string(data), value)
}
