package mcp

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestOrganizationToolsCreateAndList(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	createResp := callMCPToolForTest(t, server, "organization_create", map[string]any{"name": "Engineering", "slug": "engineering"})
	result := createResp["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	org := structured["organization"].(map[string]any)
	if org["slug"] != "engineering" {
		t.Fatalf("created org = %#v", org)
	}

	listResp := callMCPToolForTest(t, server, "organization_list", map[string]any{})
	result = listResp["result"].(map[string]any)
	structured = result["structuredContent"].(map[string]any)
	orgs := structured["organizations"].([]any)
	if len(orgs) < 2 {
		t.Fatalf("expected seeded and created orgs, got %#v", orgs)
	}

	var audited bool
	for _, call := range st.ListToolCalls() {
		if call.ToolName == "organization_create" && call.Status == "succeeded" {
			audited = true
			break
		}
	}
	if !audited {
		t.Fatalf("expected organization_create tool call record")
	}
}

func callMCPToolForTest(t *testing.T, server *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
	data, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded["error"] != nil {
		t.Fatalf("tool error: %#v", decoded["error"])
	}
	return decoded
}
