package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestManagerTimesOutRunAndReleasesCapacity(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "TIMEOUT-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Timeout test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/timeout-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "missing-timeout-profile",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	manager := NewManager(config.Config{
		ExecutorMode:         "mock",
		RunRoot:              t.TempDir(),
		WorktreeRoot:         t.TempDir(),
		RepoCacheRoot:        t.TempDir(),
		WorkerDefaultTimeout: time.Millisecond,
	}, st)
	manager.Start(context.Background(), task, run)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		updated, err := st.GetRun(run.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if updated.Status == "timed_out" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	updated, _ := st.GetRun(run.ID)
	if updated.Status != "timed_out" {
		t.Fatalf("run status = %q", updated.Status)
	}
	foundTimeoutAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.executor_timeout" && entry.ResourceID == run.ID {
			foundTimeoutAudit = true
			break
		}
	}
	if !foundTimeoutAudit {
		t.Fatalf("expected worker timeout audit log")
	}

	second := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "TIMEOUT-2",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Timeout release test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/timeout-2",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "missing-timeout-profile",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	if _, err := st.StartRun(second.ID, "feature", "docker"); err != nil {
		t.Fatalf("expected capacity to release after timeout: %v", err)
	}
}

func TestDockerNetworkModeRequiresTaskNetworkFlag(t *testing.T) {
	task := domain.Task{Envelope: domain.TaskEnvelope{}}
	if got := dockerNetworkMode(task); got != "none" {
		t.Fatalf("network mode = %q", got)
	}
	task.Envelope.Network = true
	if got := dockerNetworkMode(task); got != "bridge" {
		t.Fatalf("network mode with network flag = %q", got)
	}
}

func TestWorkerSecretEnvPlanRequiresAllowlistNetworkAndHostEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-secret")
	t.Setenv("NOT_ALLOWED", "not-allowed-secret")

	st := store.NewMemoryStore()
	_, err := st.CreateAgentProfile(domain.AgentProfile{
		ProjectID:      "proj_demo",
		Name:           "network-feature",
		Role:           "feature",
		Executor:       "docker",
		NetworkEnabled: true,
		Config: map[string]any{
			"worker_secret_env": []any{"OPENAI_API_KEY", "MISSING_KEY", "NOT_ALLOWED", "bad-name!"},
		},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	manager := NewManager(config.Config{
		WorkerSecretEnvAllowlist: []string{"OPENAI_API_KEY", "MISSING_KEY"},
	}, st)
	task := domain.Task{
		ProjectID: "proj_demo",
		Envelope: domain.TaskEnvelope{
			AgentProfile: "network-feature",
			Network:      true,
		},
	}

	plan := manager.workerSecretEnvPlan(task)
	if plan.Provider != "env" {
		t.Fatalf("provider = %q", plan.Provider)
	}
	if len(plan.Injected) != 1 || plan.Injected[0] != "OPENAI_API_KEY" {
		t.Fatalf("injected = %#v", plan.Injected)
	}
	if len(plan.Values) != 1 || plan.Values[0] != "sk-test-secret" {
		t.Fatalf("values = %#v", plan.Values)
	}
	reasons := map[string]string{}
	for _, skipped := range plan.Skipped {
		reasons[skipped["name"]] = skipped["reason"]
	}
	for name, reason := range map[string]string{
		"MISSING_KEY": "missing_host_env",
		"NOT_ALLOWED": "not_allowlisted",
		"bad-name!":   "invalid_name",
	} {
		if reasons[name] != reason {
			t.Fatalf("skip reason for %s = %q, want %q (all %#v)", name, reasons[name], reason, plan.Skipped)
		}
	}

	task.Envelope.Network = false
	plan = manager.workerSecretEnvPlan(task)
	if len(plan.Injected) != 0 {
		t.Fatalf("injected without network = %#v", plan.Injected)
	}
	if len(plan.Skipped) == 0 || plan.Skipped[0]["reason"] != "network_disabled" {
		t.Fatalf("network-disabled skip missing: %#v", plan.Skipped)
	}
}

func TestPrepareWorkspaceBootstrapsMissingSeedDemoRepository(t *testing.T) {
	st := store.NewMemoryStore()
	remote := filepath.Join(t.TempDir(), "demo-service.git")
	repo := st.CreateRepository(domain.Repository{
		ProjectID:     "proj_demo",
		Name:          "demo-service",
		Provider:      "local",
		RemoteURL:     "file://" + remote,
		DefaultBranch: "main",
	})
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "SEED-DEMO-1",
		ProjectID:       "proj_demo",
		RepositoryID:    repo.ID,
		Title:           "Seed demo repo bootstrap",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/seed-demo-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	manager := NewManager(config.Config{
		RunRoot:       t.TempDir(),
		WorktreeRoot:  t.TempDir(),
		RepoCacheRoot: t.TempDir(),
	}, st)
	workspace, err := manager.prepareWorkspace(context.Background(), task, run)
	if err != nil {
		t.Fatalf("prepare workspace: %v", err)
	}
	if _, err := os.Stat(remote); err != nil {
		t.Fatalf("seed bare repo missing: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "README.md")); err != nil || !strings.Contains(string(data), "Demo Service") {
		t.Fatalf("seed workspace README missing or unexpected: %v, %s", err, data)
	}
	foundEvent := false
	for _, event := range st.ListEvents(run.ID) {
		if event.EventType == "workspace_seed_repo_bootstrap" {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatalf("expected workspace seed bootstrap event")
	}
	foundAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.seed_repository_bootstrap" && entry.ResourceID == repo.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected seed repository bootstrap audit log")
	}
}

func TestWorkerSecretEnvPlanCanResolveFromRotatedFileProvider(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "worker-secrets.json")
	if err := os.WriteFile(secretFile, []byte(`{"OPENAI_API_KEY":"sk-file-secret-one"}`), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	st := store.NewMemoryStore()
	_, err := st.CreateAgentProfile(domain.AgentProfile{
		ProjectID:      "proj_demo",
		Name:           "file-secret-feature",
		Role:           "feature",
		Executor:       "docker",
		NetworkEnabled: true,
		Config: map[string]any{
			"worker_secret_env": []any{"OPENAI_API_KEY", "MISSING_KEY"},
		},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	manager := NewManager(config.Config{
		WorkerSecretEnvAllowlist: []string{"OPENAI_API_KEY", "MISSING_KEY"},
		WorkerSecretProvider:     "file",
		WorkerSecretFilePath:     secretFile,
	}, st)
	task := domain.Task{
		ProjectID: "proj_demo",
		Envelope: domain.TaskEnvelope{
			AgentProfile: "file-secret-feature",
			Network:      true,
		},
	}
	plan := manager.workerSecretEnvPlan(task)
	if plan.Provider != "file" {
		t.Fatalf("provider = %q", plan.Provider)
	}
	if len(plan.Injected) != 1 || plan.Injected[0] != "OPENAI_API_KEY" {
		t.Fatalf("injected = %#v", plan.Injected)
	}
	if len(plan.Values) != 1 || plan.Values[0] != "sk-file-secret-one" {
		t.Fatalf("values = %#v", plan.Values)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0]["name"] != "MISSING_KEY" || plan.Skipped[0]["reason"] != "missing_secret" {
		t.Fatalf("skipped = %#v", plan.Skipped)
	}

	if err := os.WriteFile(secretFile, []byte(`{"OPENAI_API_KEY":"sk-file-secret-two"}`), 0o600); err != nil {
		t.Fatalf("rotate secret file: %v", err)
	}
	plan = manager.workerSecretEnvPlan(task)
	if len(plan.Values) != 1 || plan.Values[0] != "sk-file-secret-two" {
		t.Fatalf("rotated values = %#v", plan.Values)
	}
}

func TestWorkerSecretEnvPlanCanResolveFromVaultProvider(t *testing.T) {
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/data/multi-codex/worker" {
			t.Fatalf("vault path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Vault-Token") != "vault-token" {
			t.Fatalf("vault token header = %q", r.Header.Get("X-Vault-Token"))
		}
		_, _ = w.Write([]byte(`{"data":{"data":{"OPENAI_API_KEY":"sk-vault-secret"}}}`))
	}))
	defer vault.Close()

	st := store.NewMemoryStore()
	_, err := st.CreateAgentProfile(domain.AgentProfile{
		ProjectID:      "proj_demo",
		Name:           "vault-secret-feature",
		Role:           "feature",
		Executor:       "docker",
		NetworkEnabled: true,
		Config: map[string]any{
			"worker_secret_env": []any{"OPENAI_API_KEY"},
		},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	manager := NewManager(config.Config{
		WorkerSecretEnvAllowlist: []string{"OPENAI_API_KEY"},
		WorkerSecretProvider:     "vault",
		WorkerVaultAddress:       vault.URL,
		WorkerVaultToken:         "vault-token",
		WorkerVaultMount:         "kv",
		WorkerVaultSecretPath:    "multi-codex/worker",
	}, st)
	task := domain.Task{
		ProjectID: "proj_demo",
		Envelope: domain.TaskEnvelope{
			AgentProfile: "vault-secret-feature",
			Network:      true,
		},
	}
	plan := manager.workerSecretEnvPlan(task)
	if plan.Provider != "vault" {
		t.Fatalf("provider = %q", plan.Provider)
	}
	if len(plan.Injected) != 1 || plan.Injected[0] != "OPENAI_API_KEY" {
		t.Fatalf("injected = %#v", plan.Injected)
	}
	if len(plan.Values) != 1 || plan.Values[0] != "sk-vault-secret" {
		t.Fatalf("values = %#v", plan.Values)
	}
}

func TestRedactWithSecretValues(t *testing.T) {
	got := redactWithSecrets("token=sk-test-secret password=keep", []string{"sk-test-secret"})
	if got == "token=sk-test-secret password=keep" {
		t.Fatalf("expected redaction")
	}
	for _, leaked := range []string{"sk-test-secret", "token", "password"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted output leaked %q: %s", leaked, got)
		}
	}
}

func TestAuditWorkerRecordsAuditLog(t *testing.T) {
	st := store.NewMemoryStore()
	manager := NewManager(config.Config{}, st)
	manager.auditWorker("run_1", "worker.secret_env_decision", "run", "run_1", map[string]any{"injected": []string{"OPENAI_API_KEY"}})

	logs := st.ListAuditLogs()
	if len(logs) == 0 {
		t.Fatalf("expected audit log")
	}
	latest := logs[0]
	if latest.Action != "worker.secret_env_decision" || latest.ActorType != "worker" || latest.ResourceID != "run_1" {
		t.Fatalf("unexpected audit log: %#v", latest)
	}
}

func TestFailQueuesRetryWhenProfileAllows(t *testing.T) {
	st := store.NewMemoryStore()
	_, err := st.CreateAgentProfile(domain.AgentProfile{
		ProjectID: "proj_demo",
		Name:      "retry-feature",
		Role:      "feature",
		Executor:  "docker",
		Config: map[string]any{
			"retry_max_attempts": 2,
			"queue_priority":     7,
		},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "RETRY-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Retry test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/retry-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "retry-feature",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	manager := NewManager(config.Config{}, st)
	manager.fail(task, run, errors.New("first attempt failed"))

	runs := st.ListRuns(task.ID)
	var queued domain.Run
	for _, candidate := range runs {
		if candidate.Status == "queued" {
			queued = candidate
			break
		}
	}
	if queued.ID == "" {
		t.Fatalf("expected queued retry run, runs = %#v", runs)
	}
	if got := intFromResult(queued.Result, "retry_attempt", 0); got != 2 {
		t.Fatalf("retry attempt = %d", got)
	}
	if got := intFromResult(queued.Result, "queue_priority", 0); got != 7 {
		t.Fatalf("queue priority = %d", got)
	}
	foundAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.retry_queued" && entry.ResourceID == queued.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected retry queued audit log")
	}
}

func TestSSHCommandArgsParseAddressAndConfig(t *testing.T) {
	args := sshCommandArgs(domain.ExecutorNode{
		Address:       "codex-worker@example.com:2222",
		ForcedCommand: "multi-codex-worker-agentd --forced-command",
	}, config.Config{
		SSHPrivateKeyPath: "/tmp/key",
		SSHKnownHostsPath: "/tmp/known_hosts",
		SSHConnectTimeout: 2500 * time.Millisecond,
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-T",
		"BatchMode=yes",
		"StrictHostKeyChecking=yes",
		"ConnectTimeout=3",
		"-i /tmp/key",
		"UserKnownHostsFile=/tmp/known_hosts",
		"-p 2222",
		"codex-worker@example.com",
		"multi-codex-worker-agentd --forced-command",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ssh args missing %q: %#v", want, args)
		}
	}
}

func TestRunSSHForcedCommandCollectsResultLogAndAudit(t *testing.T) {
	tempDir := t.TempDir()
	fakeSSH := filepath.Join(tempDir, "ssh")
	payloadPath := filepath.Join(tempDir, "payload.json")
	script := `#!/bin/sh
cat > "` + payloadPath + `"
printf '%s\n' '{"status":"succeeded","summary":"forced ok","worker_log_content":"remote log line\n"}'
printf '%s\n' 'remote stderr line' >&2
`
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := store.NewMemoryStore()
	node, err := st.RegisterExecutorNode(domain.ExecutorNode{
		Kind:               "ssh",
		Name:               "forced-test",
		Address:            "codex-worker@example.invalid:22",
		HostKeyFingerprint: "SHA256:test",
		HostKeyVerified:    true,
		ForcedCommand:      "multi-codex-worker-agentd --forced-command",
		Status:             "active",
		Capacity:           map[string]any{"concurrency": 1},
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "SSH-FORCED-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "SSH forced command test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/ssh-forced-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "ssh",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "ssh")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.ExecutorNodeID != node.ID {
		t.Fatalf("executor node = %q, want %q", run.ExecutorNodeID, node.ID)
	}
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte("verify forced command"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(config.Config{SSHConnectTimeout: time.Second}, st)
	if err := manager.runSSH(context.Background(), task, run, runContext{RunDir: runDir, Workspace: t.TempDir()}); err != nil {
		t.Fatalf("run ssh forced command: %v", err)
	}

	updated, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != "succeeded" {
		t.Fatalf("run status = %q", updated.Status)
	}
	payloadBytes, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("payload missing: %v", err)
	}
	if !strings.Contains(string(payloadBytes), `"run_id":"`+run.ID+`"`) {
		t.Fatalf("payload did not include run id: %s", payloadBytes)
	}
	logBytes, err := os.ReadFile(filepath.Join(runDir, "worker.log"))
	if err != nil {
		t.Fatalf("worker log missing: %v", err)
	}
	if !strings.Contains(string(logBytes), "remote log line") || !strings.Contains(string(logBytes), "remote stderr line") {
		t.Fatalf("worker log did not collect remote output: %s", logBytes)
	}
	if _, err := os.Stat(filepath.Join(runDir, "remote-result.json")); err != nil {
		t.Fatalf("remote result missing: %v", err)
	}
	foundAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.ssh_forced_command_run" && entry.ResourceID == run.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected ssh forced command audit log")
	}
}

func TestRunSSHAgentDHTTPSendsBearerToken(t *testing.T) {
	st := store.NewMemoryStore()
	var requestCount int
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Header.Get("Authorization") != "Bearer agentd-secret" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/runs":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"succeeded","summary":"agentd ok","worker_log_content":"remote log line\n"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/logs"):
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("remote log line\n"))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/result"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"succeeded","summary":"remote result"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer agent.Close()

	node, err := st.RegisterExecutorNode(domain.ExecutorNode{
		Kind:               "ssh",
		Name:               "agentd-token-test",
		AgentDURL:          agent.URL,
		HostKeyFingerprint: "SHA256:test",
		HostKeyVerified:    true,
		Status:             "active",
		Capacity:           map[string]any{"concurrency": 1},
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "SSH-AGENTD-TOKEN-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "SSH agentd token test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/ssh-agentd-token-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "ssh",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "ssh")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if run.ExecutorNodeID != node.ID {
		t.Fatalf("executor node = %q, want %q", run.ExecutorNodeID, node.ID)
	}
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte("verify agentd token"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(config.Config{AgentDToken: "agentd-secret"}, st)
	if err := manager.runSSH(context.Background(), task, run, runContext{RunDir: runDir, Workspace: t.TempDir()}); err != nil {
		t.Fatalf("run ssh agentd http: %v", err)
	}
	if requestCount < 3 {
		t.Fatalf("expected post/log/result requests, got %d", requestCount)
	}
	if _, err := os.Stat(filepath.Join(runDir, "remote-result.json")); err != nil {
		t.Fatalf("remote result missing: %v", err)
	}
	foundAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.ssh_agentd_run" && entry.ResourceID == run.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected ssh agentd audit log")
	}
}
