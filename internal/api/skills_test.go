package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestSkillVersionHistoryAPI(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	createSkillVersion(t, server, `{"name":"company-release-worker","role":"release","description":"Release worker","version":"v1","content_hash":"hash-v1","path":"skills/company-release-worker/SKILL.md"}`)
	latest := createSkillVersion(t, server, `{"name":"company-release-worker","role":"release","description":"Release worker","version":"v2","content_hash":"hash-v2","path":"skills/company-release-worker/SKILL.md"}`)
	if latest.LatestVersion != "v2" || latest.ContentHash != "hash-v2" {
		t.Fatalf("latest skill = %#v", latest)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skills/"+latest.ID+"/versions", nil)
	req.AddCookie(localSessionCookie(t, server.Handler()))
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("versions status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var versions []domain.SkillVersion
	if err := json.Unmarshal(resp.Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %#v", versions)
	}
	seen := map[string]string{}
	for _, version := range versions {
		seen[version.Version] = version.ContentHash
	}
	if seen["v1"] != "hash-v1" || seen["v2"] != "hash-v2" {
		t.Fatalf("version hashes = %#v", seen)
	}
}

func createSkillVersion(t *testing.T, server *Server, body string) domain.Skill {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skills", bytes.NewBufferString(body))
	req.AddCookie(localSessionCookie(t, server.Handler()))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("create skill status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var skill domain.Skill
	if err := json.Unmarshal(resp.Body.Bytes(), &skill); err != nil {
		t.Fatal(err)
	}
	return skill
}
