package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
)

type runRequest struct {
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id"`
	Role   string `json:"role"`
	Prompt string `json:"prompt"`
}

type runResult struct {
	Status           string    `json:"status"`
	RunID            string    `json:"run_id"`
	TaskID           string    `json:"task_id"`
	Role             string    `json:"role"`
	Summary          string    `json:"summary"`
	WorkerLog        string    `json:"worker_log"`
	WorkerLogContent string    `json:"worker_log_content,omitempty"`
	ResultPath       string    `json:"result_path"`
	CompletedAt      time.Time `json:"completed_at"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv()
	server := &agentServer{runRoot: cfg.RunRoot, token: cfg.AgentDToken, log: log}
	if len(os.Args) > 1 && os.Args[1] == "--forced-command" {
		if err := server.forcedCommand(os.Stdin, os.Stdout); err != nil {
			log.Error("forced command failed", "error", err)
			os.Exit(1)
		}
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("POST /v1/runs", server.createRun)
	mux.HandleFunc("GET /v1/runs/", server.getRunFile)

	log.Info("worker agentd listening", "addr", cfg.AgentDListen, "run_root", cfg.RunRoot)
	if err := http.ListenAndServe(cfg.AgentDListen, mux); err != nil {
		log.Error("worker agentd failed", "error", err)
		os.Exit(1)
	}
}

type agentServer struct {
	runRoot string
	token   string
	log     *slog.Logger
}

func (s *agentServer) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "multi-codex-worker-agentd"})
}

func (s *agentServer) createRun(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	defer r.Body.Close()
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.RunID == "" || req.TaskID == "" || req.Role == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id, task_id, and role are required"})
		return
	}

	result, err := s.recordRun(req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("agentd run recorded", "run_id", req.RunID, "task_id", req.TaskID, "role", req.Role)
	writeJSON(w, http.StatusCreated, result)
}

func (s *agentServer) forcedCommand(input *os.File, output *os.File) error {
	var req runRequest
	if err := json.NewDecoder(input).Decode(&req); err != nil {
		return err
	}
	if req.RunID == "" || req.TaskID == "" || req.Role == "" {
		return fmt.Errorf("run_id, task_id, and role are required")
	}
	result, err := s.recordRun(req)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func (s *agentServer) recordRun(req runRequest) (runResult, error) {
	runDir := filepath.Join(s.runRoot, safePath(req.RunID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return runResult{}, err
	}
	promptPath := filepath.Join(runDir, "prompt.md")
	logPath := filepath.Join(runDir, "worker.log")
	resultPath := filepath.Join(runDir, "result.json")

	if err := os.WriteFile(promptPath, []byte(req.Prompt), 0o644); err != nil {
		return runResult{}, err
	}
	logBody := fmt.Sprintf("agentd accepted controlled run role=%s task=%s\n", req.Role, req.TaskID)
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		return runResult{}, err
	}
	result := runResult{
		Status:           "succeeded",
		RunID:            req.RunID,
		TaskID:           req.TaskID,
		Role:             req.Role,
		Summary:          "worker-agentd accepted and recorded a controlled remote run payload",
		WorkerLog:        logPath,
		WorkerLogContent: logBody,
		ResultPath:       resultPath,
		CompletedAt:      time.Now().UTC(),
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(resultPath, append(data, '\n'), 0o644); err != nil {
		return runResult{}, err
	}
	return result, nil
}

func (s *agentServer) getRunFile(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id is required"})
		return
	}
	var name string
	var contentType string
	switch parts[1] {
	case "result":
		name = "result.json"
		contentType = "application/json"
	case "logs":
		name = "worker.log"
		contentType = "text/plain; charset=utf-8"
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run file not found"})
		return
	}
	data, err := os.ReadFile(filepath.Join(s.runRoot, safePath(parts[0]), name))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run file not found"})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *agentServer) authorize(w http.ResponseWriter, r *http.Request) bool {
	if s.token == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + s.token
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1 {
		return true
	}
	s.log.Warn("agentd request denied", "method", r.Method, "path", r.URL.Path, "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "agentd authorization required"})
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func safePath(value string) string {
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, ":", "_")
	return value
}
