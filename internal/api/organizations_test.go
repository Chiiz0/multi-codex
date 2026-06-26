package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestOrganizationsAPIProvisioningAuditsCreate(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", strings.NewReader(`{"name":"Engineering","slug":"engineering"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var org domain.Organization
	if err := json.Unmarshal(resp.Body.Bytes(), &org); err != nil {
		t.Fatalf("decode org: %v", err)
	}
	if org.ID == "" || org.Slug != "engineering" {
		t.Fatalf("organization = %#v", org)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var orgs []domain.Organization
	if err := json.Unmarshal(resp.Body.Bytes(), &orgs); err != nil {
		t.Fatalf("decode orgs: %v", err)
	}
	if len(orgs) < 2 {
		t.Fatalf("expected seeded and created orgs, got %#v", orgs)
	}

	var audited bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.organization_create" && entry.ResourceID == org.ID {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatalf("expected api.organization_create audit row")
	}
}

func TestOrganizationsAPIRejectsDuplicateSlug(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", strings.NewReader(`{"name":"Engineering","slug":"engineering"}`))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.Handler().ServeHTTP(resp, req)
		if i == 0 && resp.Code != http.StatusCreated {
			t.Fatalf("first create status = %d, body = %s", resp.Code, resp.Body.String())
		}
		if i == 1 && resp.Code != http.StatusConflict {
			t.Fatalf("duplicate status = %d, body = %s", resp.Code, resp.Body.String())
		}
	}
}
