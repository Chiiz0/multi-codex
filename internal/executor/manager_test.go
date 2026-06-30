package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestDockerRunArgsApplyIsolationLimits(t *testing.T) {
	args := dockerRunArgs(
		config.Config{WorkerImage: "multi-codex/codex-worker:test"},
		domain.Task{ID: "task_1"},
		domain.Run{ID: "run_1", Role: "feature"},
		runContext{RunDir: "/runs/run_1", Workspace: "/worktrees/run_1"},
		"multi-codex-run_1",
		"none",
		dockerResourcePolicy{
			CPUs:            "1.5",
			Memory:          "1536m",
			PidsLimit:       128,
			ReadOnlyRootFS:  true,
			TmpfsSize:       "64m",
			NoNewPrivileges: true,
			CapDrop:         []string{"ALL"},
		},
		[]string{"OPENAI_API_KEY"},
		"echo ok",
	)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--network none",
		"--cpus 1.5",
		"--memory 1536m",
		"--pids-limit 128",
		"--security-opt no-new-privileges",
		"--cap-drop ALL",
		"--read-only",
		"--tmpfs /tmp:rw,noexec,nosuid,size=64m",
		"--tmpfs /home/codex:rw,nosuid,size=64m",
		"-v /runs/run_1:/runs:rw",
		"-v /worktrees/run_1:/workspace:rw",
		"-e OPENAI_API_KEY",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("docker args missing %q: %#v", want, args)
		}
	}
}

func TestWorkerResourcePolicyCanUseProfileOverrides(t *testing.T) {
	st := store.NewMemoryStore()
	_, err := st.CreateAgentProfile(domain.AgentProfile{
		ProjectID: "proj_demo",
		Name:      "resource-feature",
		Role:      "feature",
		Executor:  "docker",
		Config: map[string]any{
			"worker_resources": map[string]any{
				"cpus":             "0.75",
				"memory":           "768m",
				"pids_limit":       96,
				"read_only_rootfs": true,
				"tmpfs_size":       "96m",
			},
		},
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	manager := NewManager(config.Config{
		WorkerCPUs:            "2",
		WorkerMemory:          "4g",
		WorkerPidsLimit:       512,
		WorkerReadOnlyRootFS:  true,
		WorkerTmpfsSize:       "512m",
		WorkerNoNewPrivileges: true,
		WorkerCapDrop:         []string{"ALL"},
	}, st)
	policy := manager.workerResourcePolicy(domain.Task{
		ProjectID: "proj_demo",
		Envelope:  domain.TaskEnvelope{AgentProfile: "resource-feature"},
	})
	if policy.CPUs != "0.75" || policy.Memory != "768m" || policy.PidsLimit != 96 || policy.TmpfsSize != "96m" {
		t.Fatalf("resource policy = %#v", policy)
	}
}

func TestWorkerCommandPolicyBlocksDeniedCommands(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "CMD-POLICY-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Command policy test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/cmd-policy-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./...", "git push origin main"},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	manager := NewManager(config.Config{WorkerCommandDenylist: []string{"git push"}}, st)
	if !manager.enforceWorkerPlanPolicy(task, run) {
		t.Fatalf("expected worker command policy to block")
	}
	updated, err := st.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != "blocked" {
		t.Fatalf("run status = %q", updated.Status)
	}
	foundAudit := false
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.command_policy" && entry.ResourceID == run.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected command policy audit log")
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

func TestCollectArtifactsRecordsDependencyPolicyBlock(t *testing.T) {
	workspace := t.TempDir()
	for _, command := range [][]string{
		{"init"},
		{"config", "user.email", "multi-codex@example.invalid"},
		{"config", "user.name", "multi-codex test"},
	} {
		runGit(t, workspace, command...)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workspace, "add", ".")
	runGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n\nrequire example.com/pkg v1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.NewMemoryStore()
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "DEP-POLICY-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Dependency policy test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/dep-policy-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
		Policy:          domain.TaskPolicy{AllowDependencyChange: false},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	runDir := t.TempDir()
	for name, content := range map[string]string{
		"worker.log":  "done\n",
		"result.json": "{}\n",
		"diff.patch":  "",
	} {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(config.Config{}, st)
	if err := manager.collectArtifactsAndScope(context.Background(), task, run, runContext{RunDir: runDir, Workspace: workspace}); err != nil {
		t.Fatalf("collect artifacts: %v", err)
	}
	updatedTask, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updatedTask.Status != "blocked" {
		t.Fatalf("task status = %q", updatedTask.Status)
	}
	foundEvent := false
	foundAudit := false
	for _, event := range st.ListEvents(run.ID) {
		if event.EventType == "dependency_policy" && event.Payload["status"] == "blocked" {
			foundEvent = true
		}
	}
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "worker.dependency_policy" && entry.ResourceID == run.ID {
			foundAudit = true
			break
		}
	}
	if !foundEvent || !foundAudit {
		t.Fatalf("expected dependency policy event=%v audit=%v", foundEvent, foundAudit)
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

func TestRedactDetectsStructuredSecrets(t *testing.T) {
	input := "github=ghp_abcdefghijklmnopqrstuvwxyz aws=AKIA1234567890ABCDEF jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature123456789 password=hunter2"
	got := redact(input)
	for _, leaked := range []string{"ghp_abcdefghijklmnopqrstuvwxyz", "AKIA1234567890ABCDEF", "eyJhbGciOiJIUzI1NiJ9", "hunter2", "password"} {
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, output)
	}
}
