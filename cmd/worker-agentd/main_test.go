package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentDHTTPBearerTokenProtectsRunAndFiles(t *testing.T) {
	server := &agentServer{
		runRoot: t.TempDir(),
		token:   "agentd-secret",
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", server.createRun)
	mux.HandleFunc("GET /v1/runs/", server.getRunFile)

	payload := []byte(`{"run_id":"run-agentd-auth","task_id":"task-1","role":"feature","prompt":"verify"}`)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(payload))
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized create status = %d, body = %s", resp.Code, resp.Body.String())
	}

	resp = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer agentd-secret")
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("authorized create status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var result runResult
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(result.Summary), "poc") || result.Status != "succeeded" {
		t.Fatalf("unexpected result = %#v", result)
	}

	resp = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/run-agentd-auth/logs", nil)
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized logs status = %d, body = %s", resp.Code, resp.Body.String())
	}

	resp = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/run-agentd-auth/logs", nil)
	req.Header.Set("Authorization", "Bearer agentd-secret")
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "agentd accepted controlled run") {
		t.Fatalf("authorized logs status = %d, body = %s", resp.Code, resp.Body.String())
	}
}
