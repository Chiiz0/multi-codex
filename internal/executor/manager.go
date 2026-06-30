package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/policy"
	"github.com/Chiiz0/multi-codex/internal/secrets"
	"github.com/Chiiz0/multi-codex/internal/store"
)

type Manager struct {
	cfg            config.Config
	store          store.Store
	secretResolver secrets.Resolver
}

type runContext struct {
	RunDir    string
	Workspace string
	Branch    string
}

type workerSecretEnvPlan struct {
	Requested []string
	Injected  []string
	Values    []string
	Skipped   []map[string]string
	Provider  string
}

func NewManager(cfg config.Config, st store.Store) *Manager {
	resolver, err := secrets.NewResolverWithConfig(secrets.ResolverConfig{
		Provider:        cfg.WorkerSecretProvider,
		FilePath:        cfg.WorkerSecretFilePath,
		VaultAddress:    cfg.WorkerVaultAddress,
		VaultToken:      cfg.WorkerVaultToken,
		VaultTokenFile:  cfg.WorkerVaultTokenFile,
		VaultNamespace:  cfg.WorkerVaultNamespace,
		VaultMount:      cfg.WorkerVaultMount,
		VaultSecretPath: cfg.WorkerVaultSecretPath,
	})
	if err != nil {
		resolver = secrets.UnavailableResolver{ProviderName: cfg.WorkerSecretProvider, Err: err}
	}
	return &Manager{cfg: cfg, store: st, secretResolver: resolver}
}

func (m *Manager) Start(ctx context.Context, task domain.Task, run domain.Run) {
	timeout := m.runTimeout(task)
	go func() {
		runCtx := context.Background()
		if ctx != nil {
			runCtx = context.WithoutCancel(ctx)
		}
		var cancel context.CancelFunc = func() {}
		if timeout > 0 {
			runCtx, cancel = context.WithTimeout(runCtx, timeout)
		}
		defer cancel()
		m.execute(runCtx, task, run, timeout)
	}()
}

func (m *Manager) execute(ctx context.Context, task domain.Task, run domain.Run, timeout time.Duration) {
	if timeout > 0 {
		_, _ = m.store.AddEvent(run.ID, "info", "executor_timeout_policy", "Worker timeout policy applied", timeoutPayload(timeout))
	}
	if m.enforceWorkerPlanPolicy(task, run) {
		return
	}
	rc, err := m.prepareRun(ctx, task, run)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			m.timeout(run.ID, timeout)
			return
		}
		m.fail(task, run, err)
		return
	}

	if run.Executor == "ssh" || task.Envelope.Executor == "ssh" {
		err = m.runSSH(ctx, task, run, rc)
	} else {
		switch m.cfg.ExecutorMode {
		case "docker":
			err = m.runDocker(ctx, task, run, rc)
		default:
			err = m.runLocalLifecycle(ctx, task, run, rc)
		}
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			m.timeout(run.ID, timeout)
			return
		}
		m.fail(task, run, err)
		return
	}

	if err := m.collectArtifactsAndScope(ctx, task, run, rc); err != nil {
		_, _ = m.store.AddEvent(run.ID, "warn", "artifact_collect_warning", err.Error(), nil)
	}
}

func (m *Manager) prepareRun(ctx context.Context, task domain.Task, run domain.Run) (runContext, error) {
	runDir := filepath.Join(m.cfg.RunRoot, run.ID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return runContext{}, err
	}
	rc := runContext{RunDir: runDir, Branch: task.Envelope.TargetBranch}

	if err := writeJSON(filepath.Join(runDir, "task.json"), task.Envelope); err != nil {
		return runContext{}, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte(renderPrompt(task, run)), 0o644); err != nil {
		return runContext{}, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "AGENTS.override.md"), []byte(renderAgentOverride(task, run)), 0o644); err != nil {
		return runContext{}, err
	}
	_, _ = m.recordArtifact(run.ID, runDir, "task_envelope", "task.json")
	_, _ = m.recordArtifact(run.ID, runDir, "prompt", "prompt.md")
	_, _ = m.recordArtifact(run.ID, runDir, "agent_override", "AGENTS.override.md")

	workspace, err := m.prepareWorkspace(ctx, task, run)
	if err != nil {
		_, _ = m.store.AddEvent(run.ID, "warn", "workspace_prepare_failed", err.Error(), nil)
		workspace = filepath.Join(m.cfg.WorktreeRoot, run.ID)
		if mkdirErr := os.MkdirAll(workspace, 0o755); mkdirErr != nil {
			return runContext{}, mkdirErr
		}
	}
	rc.Workspace = workspace
	_, _ = m.store.UpdateRunWorkspace(run.ID, rc.Branch, rc.Workspace)
	_, _ = m.store.AddEvent(run.ID, "info", "executor_prepare", "Run directory and workspace prepared", map[string]any{
		"run_dir":   rc.RunDir,
		"workspace": rc.Workspace,
		"branch":    rc.Branch,
	})
	return rc, nil
}

func (m *Manager) prepareWorkspace(ctx context.Context, task domain.Task, run domain.Run) (string, error) {
	repo, err := m.store.GetRepository(task.RepositoryID)
	if err != nil {
		return "", err
	}
	workspace := filepath.Join(m.cfg.WorktreeRoot, run.ID)
	if err := os.MkdirAll(m.cfg.RepoCacheRoot, 0o755); err != nil {
		return "", err
	}
	if err := os.RemoveAll(workspace); err != nil {
		return "", err
	}

	remoteURL := repo.RemoteURL
	if strings.HasPrefix(remoteURL, "file://") {
		remoteURL = strings.TrimPrefix(remoteURL, "file://")
		if seededRemote, err := m.ensureSeedDemoRemote(ctx, repo, remoteURL, run.ID); err != nil {
			return "", err
		} else if seededRemote != "" {
			remoteURL = seededRemote
		}
	}
	if remoteURL == "" {
		return "", fmt.Errorf("repository %s has no remote_url", repo.ID)
	}

	mirror := repo.LocalMirrorPath
	if mirror == "" {
		mirror = filepath.Join(m.cfg.RepoCacheRoot, safePathComponent(repo.ID)+".git")
	}
	if _, err := os.Stat(mirror); os.IsNotExist(err) {
		if output, err := exec.CommandContext(ctx, "git", "clone", "--mirror", remoteURL, mirror).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git clone --mirror failed: %w: %s", err, redact(string(output)))
		}
	} else {
		if output, err := exec.CommandContext(ctx, "git", "--git-dir", mirror, "fetch", "--prune").CombinedOutput(); err != nil {
			return "", fmt.Errorf("git mirror fetch failed: %w: %s", err, redact(string(output)))
		}
	}

	if output, err := exec.CommandContext(ctx, "git", "clone", "--shared", mirror, workspace).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone workspace failed: %w: %s", err, redact(string(output)))
	}
	base := strings.TrimPrefix(task.Envelope.BaseBranch, "origin/")
	if base == "" {
		base = repo.DefaultBranch
	}
	if output, err := exec.CommandContext(ctx, "git", "-C", workspace, "checkout", "-B", task.Envelope.TargetBranch, "origin/"+base).CombinedOutput(); err != nil {
		if output2, err2 := exec.CommandContext(ctx, "git", "-C", workspace, "checkout", "-B", task.Envelope.TargetBranch).CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("git checkout worktree failed: %w: %s / fallback: %w: %s", err, redact(string(output)), err2, redact(string(output2)))
		}
	}
	return workspace, nil
}

func (m *Manager) ensureSeedDemoRemote(ctx context.Context, repo domain.Repository, remotePath string, runID string) (string, error) {
	if remotePath == "" {
		return remotePath, nil
	}
	resolved := resolveWorkspacePath(remotePath, m.cfg.RepoCacheRoot)
	if _, err := os.Stat(resolved); err == nil {
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if !isSeedDemoRepository(repo, resolved) {
		return remotePath, nil
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return "", err
	}
	source, err := os.MkdirTemp(m.cfg.RepoCacheRoot, "seed-demo-source-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(source)
	if err := writeSeedDemoRepository(ctx, source, resolved); err != nil {
		return "", err
	}
	payload := map[string]any{
		"repository_id": repo.ID,
		"repository":    repo.Name,
		"remote_path":   resolved,
	}
	_, _ = m.store.AddEvent(runID, "info", "workspace_seed_repo_bootstrap", "Seed demo repository bootstrapped for local verification", payload)
	m.auditWorker(runID, "worker.seed_repository_bootstrap", "repository", repo.ID, payload)
	return resolved, nil
}

func resolveWorkspacePath(path string, repoCacheRoot string) string {
	if strings.HasPrefix(path, "/workspace/") {
		if wd, err := os.Getwd(); err == nil && wd != "" && wd != "/workspace" {
			if repoCacheRoot != "" {
				return filepath.Join(repoCacheRoot, "seed-remotes", strings.TrimPrefix(path, "/workspace/"))
			}
			return filepath.Join(wd, strings.TrimPrefix(path, "/workspace/"))
		}
	}
	return path
}

func isSeedDemoRepository(repo domain.Repository, remotePath string) bool {
	return repo.Provider == "local" && repo.Name == "demo-service" && strings.HasSuffix(filepath.ToSlash(remotePath), "/demo-service.git")
}

func writeSeedDemoRepository(ctx context.Context, source string, barePath string) error {
	for _, command := range [][]string{
		{"init"},
		{"config", "user.email", "multi-codex@example.invalid"},
		{"config", "user.name", "multi-codex seed"},
	} {
		if output, err := exec.CommandContext(ctx, "git", append([]string{"-C", source}, command...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("seed demo git %s failed: %w: %s", strings.Join(command, " "), err, redact(string(output)))
		}
	}
	if err := os.MkdirAll(filepath.Join(source, "internal", "demo"), 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"README.md":             "# Demo Service\n\nSeed repository used by multi-codex local executor verification.\n",
		"internal/demo/demo.go": "package demo\n\nfunc Message() string { return \"multi-codex demo\" }\n",
		"internal/demo/demo_test.go": `package demo

import "testing"

func TestMessage(t *testing.T) {
	if Message() == "" {
		t.Fatal("message should not be empty")
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	for _, command := range [][]string{
		{"add", "."},
		{"commit", "-m", "seed demo service"},
		{"branch", "-M", "main"},
		{"clone", "--bare", source, barePath},
	} {
		args := command
		if command[0] != "clone" {
			args = append([]string{"-C", source}, command...)
		}
		if output, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("seed demo git %s failed: %w: %s", strings.Join(command, " "), err, redact(string(output)))
		}
	}
	return nil
}

func (m *Manager) runLocalLifecycle(ctx context.Context, task domain.Task, run domain.Run, rc runContext) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}

	logLine := fmt.Sprintf("local executor completed role=%s task=%s workspace=%s\n", run.Role, task.TaskKey, rc.Workspace)
	if err := os.WriteFile(filepath.Join(rc.RunDir, "worker.log"), []byte(logLine), 0o644); err != nil {
		return err
	}

	changedFiles := gitChangedFiles(ctx, rc.Workspace)
	result := map[string]any{
		"status":        "done",
		"changed_files": changedFiles,
		"summary":       localSummary(run.Role),
		"tests_run":     testsForRole(task, run.Role),
		"tests_failed":  []string{},
		"risks":         localRisks(run.Role),
		"needs_human":   []string{},
		"task_id":       task.ID,
		"task_key":      task.TaskKey,
		"role":          run.Role,
		"workspace":     rc.Workspace,
	}
	if err := writeJSON(filepath.Join(rc.RunDir, "result.json"), result); err != nil {
		return err
	}
	if err := writeDiff(ctx, rc.Workspace, filepath.Join(rc.RunDir, "diff.patch")); err != nil {
		_, _ = m.store.AddEvent(run.ID, "warn", "diff_collect_failed", err.Error(), nil)
	}

	_, _ = m.store.AddEvent(run.ID, "info", "worker_log", "Local executor lifecycle completed", map[string]any{"executor_mode": m.cfg.ExecutorMode})
	m.auditWorker(run.ID, "worker.local_lifecycle", "run", run.ID, map[string]any{
		"executor_mode": m.cfg.ExecutorMode,
		"status":        "succeeded",
	})
	_, err := m.store.FinishRun(run.ID, "succeeded", result)
	return err
}

func (m *Manager) enforceWorkerPlanPolicy(task domain.Task, run domain.Run) bool {
	result := policy.CheckCommandPolicy(task.Envelope.AllowedCommands, m.cfg.WorkerCommandAllowlist, m.cfg.WorkerCommandDenylist)
	payload := map[string]any{
		"status":           result.Status,
		"allowed_commands": result.AllowedCommands,
		"violations":       result.Violations,
		"allowlist_active": result.AllowlistActive,
	}
	level := "info"
	message := "Worker command policy passed"
	if result.Status == "blocked" {
		level = "warn"
		message = "Worker command policy blocked execution"
	}
	_, _ = m.store.AddEvent(run.ID, level, "worker_command_policy", message, payload)
	m.auditWorker(run.ID, "worker.command_policy", "run", run.ID, payload)
	if result.Status != "blocked" {
		return false
	}
	_, _ = m.store.UpdateTaskStatus(task.ID, "blocked")
	_, _ = m.store.FinishRun(run.ID, "blocked", map[string]any{
		"status":     "blocked",
		"policy":     "worker_command_policy",
		"violations": result.Violations,
	})
	return true
}

func (m *Manager) runDocker(ctx context.Context, task domain.Task, run domain.Run, rc runContext) error {
	containerName := "multi-codex-" + safePathComponent(run.ID)
	networkMode := dockerNetworkMode(task)
	resourcePolicy := m.workerResourcePolicy(task)
	resourcePayload := resourcePolicy.payload()
	_, _ = m.store.AddEvent(run.ID, "info", "worker_resource_policy", "Docker worker resource and filesystem limits applied", resourcePayload)
	m.auditWorker(run.ID, "worker.resource_policy", "run", run.ID, resourcePayload)
	networkPayload := map[string]any{
		"requested": task.Envelope.Network,
		"mode":      networkMode,
	}
	_, _ = m.store.AddEvent(run.ID, "info", "worker_network_policy", "Docker worker network policy applied", networkPayload)
	m.auditWorker(run.ID, "worker.network_policy", "run", run.ID, networkPayload)
	secretPlan := m.workerSecretEnvPlan(task)
	if len(secretPlan.Requested) > 0 || len(secretPlan.Injected) > 0 || len(secretPlan.Skipped) > 0 {
		payload := map[string]any{
			"requested": secretPlan.Requested,
			"injected":  secretPlan.Injected,
			"skipped":   secretPlan.Skipped,
			"provider":  secretPlan.Provider,
		}
		_, _ = m.store.AddEvent(run.ID, "info", "worker_secret_env", "Worker secret environment decision recorded", payload)
		m.auditWorker(run.ID, "worker.secret_env_decision", "run", run.ID, payload)
	}
	command := dockerWorkerCommand(task, run)
	args := dockerRunArgs(m.cfg, task, run, rc, containerName, networkMode, resourcePolicy, secretPlan.Injected, command)
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	m.redactRunFiles(rc.RunDir, secretPlan.Values)
	_, _ = m.store.AddEvent(run.ID, "info", "docker_run", "Docker worker exited", map[string]any{
		"output":   redactWithSecrets(string(output), secretPlan.Values),
		"network":  networkMode,
		"env_vars": secretPlan.Injected,
	})
	dockerPayload := map[string]any{
		"network":  networkMode,
		"env_vars": secretPlan.Injected,
		"status":   dockerRunAuditStatus(ctx, err),
	}
	if err != nil {
		dockerPayload["error"] = redactWithSecrets(err.Error(), secretPlan.Values)
	}
	m.auditWorker(run.ID, "worker.docker_run", "run", run.ID, dockerPayload)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			m.cleanupDockerContainer(run.ID, containerName)
			return context.DeadlineExceeded
		}
		return fmt.Errorf("docker worker failed: %w", err)
	}

	resultBytes, err := os.ReadFile(filepath.Join(rc.RunDir, "result.json"))
	if err != nil {
		return err
	}
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return err
	}
	status := "succeeded"
	if resultStatus, ok := result["run_status"].(string); ok && resultStatus != "" {
		status = resultStatus
	}
	_, err = m.store.FinishRun(run.ID, status, result)
	return err
}

func (m *Manager) auditWorker(actorID string, action string, resourceType string, resourceID string, payload map[string]any) {
	m.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "worker",
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Payload:      payload,
	})
}

func dockerRunAuditStatus(ctx context.Context, err error) string {
	if err == nil {
		return "exited"
	}
	if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timed_out"
	}
	return "failed"
}

type dockerResourcePolicy struct {
	CPUs            string
	Memory          string
	PidsLimit       int
	ReadOnlyRootFS  bool
	TmpfsSize       string
	NoNewPrivileges bool
	CapDrop         []string
}

func (p dockerResourcePolicy) payload() map[string]any {
	return map[string]any{
		"cpus":               p.CPUs,
		"memory":             p.Memory,
		"pids_limit":         p.PidsLimit,
		"read_only_rootfs":   p.ReadOnlyRootFS,
		"tmpfs_size":         p.TmpfsSize,
		"no_new_privileges":  p.NoNewPrivileges,
		"cap_drop":           p.CapDrop,
		"filesystem_mounts":  []string{"/runs:rw", "/workspace:rw", "/tmp:tmpfs", "/home/codex:tmpfs"},
		"executor_isolation": "docker",
	}
}

func (m *Manager) workerResourcePolicy(task domain.Task) dockerResourcePolicy {
	policy := dockerResourcePolicy{
		CPUs:            m.cfg.WorkerCPUs,
		Memory:          m.cfg.WorkerMemory,
		PidsLimit:       m.cfg.WorkerPidsLimit,
		ReadOnlyRootFS:  m.cfg.WorkerReadOnlyRootFS,
		TmpfsSize:       m.cfg.WorkerTmpfsSize,
		NoNewPrivileges: m.cfg.WorkerNoNewPrivileges,
		CapDrop:         append([]string(nil), m.cfg.WorkerCapDrop...),
	}
	for _, profile := range m.store.ListAgentProfiles(task.ProjectID) {
		if profile.Name != task.Envelope.AgentProfile && profile.ID != task.Envelope.AgentProfile {
			continue
		}
		cfg := profile.Config
		if nested, ok := cfg["worker_resources"].(map[string]any); ok {
			cfg = mergeConfigMaps(cfg, nested)
		}
		if value := stringFromConfigMap(cfg, "worker_cpus", "cpus", "cpu_limit"); value != "" {
			policy.CPUs = value
		}
		if value := stringFromConfigMap(cfg, "worker_memory", "memory", "memory_limit"); value != "" {
			policy.Memory = value
		}
		if value := intFromConfigMapAnyKey(cfg, 0, "worker_pids_limit", "pids_limit"); value > 0 {
			policy.PidsLimit = value
		}
		if value, ok := boolFromConfigMapAnyKey(cfg, "worker_read_only_rootfs", "read_only_rootfs"); ok {
			policy.ReadOnlyRootFS = value
		}
		if value := stringFromConfigMap(cfg, "worker_tmpfs_size", "tmpfs_size"); value != "" {
			policy.TmpfsSize = value
		}
		return policy
	}
	return policy
}

func dockerRunArgs(cfg config.Config, task domain.Task, run domain.Run, rc runContext, containerName string, networkMode string, resourcePolicy dockerResourcePolicy, injectedEnv []string, command string) []string {
	args := []string{
		"run", "--rm",
		"--name", containerName,
		"--network", networkMode,
		"--workdir", "/workspace",
	}
	if strings.TrimSpace(resourcePolicy.CPUs) != "" {
		args = append(args, "--cpus", resourcePolicy.CPUs)
	}
	if strings.TrimSpace(resourcePolicy.Memory) != "" {
		args = append(args, "--memory", resourcePolicy.Memory)
	}
	if resourcePolicy.PidsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(resourcePolicy.PidsLimit))
	}
	if resourcePolicy.NoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	for _, cap := range resourcePolicy.CapDrop {
		cap = strings.TrimSpace(cap)
		if cap != "" {
			args = append(args, "--cap-drop", cap)
		}
	}
	if resourcePolicy.ReadOnlyRootFS {
		args = append(args, "--read-only")
		tmpfsSize := resourcePolicy.TmpfsSize
		if strings.TrimSpace(tmpfsSize) == "" {
			tmpfsSize = "256m"
		}
		args = append(args,
			"--tmpfs", "/tmp:rw,noexec,nosuid,size="+tmpfsSize,
			"--tmpfs", "/home/codex:rw,nosuid,size="+tmpfsSize,
		)
	}
	args = append(args,
		"-v", rc.RunDir+":/runs:rw",
		"-v", rc.Workspace+":/workspace:rw",
		"-e", "MULTICODEX_RUN_ID="+run.ID,
		"-e", "MULTICODEX_TASK_ID="+task.ID,
		"-e", "MULTICODEX_ROLE="+run.Role,
	)
	for _, name := range injectedEnv {
		args = append(args, "-e", name)
	}
	args = append(args,
		cfg.WorkerImage,
		"bash", "-lc", command,
	)
	return args
}

func (m *Manager) workerSecretEnvPlan(task domain.Task) workerSecretEnvPlan {
	requested := m.requestedWorkerSecretEnv(task)
	provider := "env"
	if m.secretResolver != nil {
		provider = m.secretResolver.Provider()
	}
	plan := workerSecretEnvPlan{Requested: requested, Provider: provider}
	if len(requested) == 0 {
		return plan
	}
	allowlist := stringSet(m.cfg.WorkerSecretEnvAllowlist)
	for _, name := range requested {
		if !validEnvName(name) {
			plan.Skipped = append(plan.Skipped, map[string]string{"name": name, "reason": "invalid_name"})
			continue
		}
		if !allowlist[name] {
			plan.Skipped = append(plan.Skipped, map[string]string{"name": name, "reason": "not_allowlisted"})
			continue
		}
		if !task.Envelope.Network {
			plan.Skipped = append(plan.Skipped, map[string]string{"name": name, "reason": "network_disabled"})
			continue
		}
		resolver := m.secretResolver
		if resolver == nil {
			resolver = secrets.EnvResolver{}
		}
		value, ok, err := resolver.Lookup(name)
		if err != nil {
			plan.Skipped = append(plan.Skipped, map[string]string{"name": name, "reason": "secret_provider_error", "provider": resolver.Provider()})
			continue
		}
		if !ok || value == "" {
			reason := "missing_secret"
			if resolver.Provider() == "env" {
				reason = "missing_host_env"
			}
			plan.Skipped = append(plan.Skipped, map[string]string{"name": name, "reason": reason, "provider": resolver.Provider()})
			continue
		}
		plan.Injected = append(plan.Injected, name)
		plan.Values = append(plan.Values, value)
	}
	return plan
}

func (m *Manager) requestedWorkerSecretEnv(task domain.Task) []string {
	for _, profile := range m.store.ListAgentProfiles(task.ProjectID) {
		if profile.Name != task.Envelope.AgentProfile && profile.ID != task.Envelope.AgentProfile {
			continue
		}
		return secretEnvNamesFromConfig(profile.Config)
	}
	return []string{}
}

func secretEnvNamesFromConfig(cfg map[string]any) []string {
	if cfg == nil {
		return []string{}
	}
	value, ok := cfg["worker_secret_env"]
	if !ok {
		value, ok = cfg["secret_env"]
	}
	if !ok {
		return []string{}
	}
	return uniqueStrings(value)
}

func uniqueStrings(value any) []string {
	values := []string{}
	seen := map[string]bool{}
	add := func(raw string) {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
		}) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			values = append(values, part)
			seen[part] = true
		}
	}
	switch typed := value.(type) {
	case string:
		add(typed)
	case []string:
		for _, item := range typed {
			add(item)
		}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				add(text)
			}
		}
	}
	return values
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	first := rune(name[0])
	return first == '_' || first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}

func (m *Manager) redactRunFiles(runDir string, secretValues []string) {
	if len(secretValues) == 0 {
		return
	}
	for _, name := range []string{"worker.log", "result.json", "diff.patch"} {
		path := filepath.Join(runDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		redacted := redactWithSecrets(string(data), secretValues)
		_ = os.WriteFile(path, []byte(redacted), 0o644)
	}
}

func dockerNetworkMode(task domain.Task) string {
	if task.Envelope.Network {
		return "bridge"
	}
	return "none"
}

func (m *Manager) runSSH(ctx context.Context, task domain.Task, run domain.Run, rc runContext) error {
	node, err := m.selectSSHNode()
	if err != nil {
		return err
	}
	if strings.TrimSpace(node.AgentDURL) != "" {
		return m.runSSHAgentDHTTP(ctx, task, run, rc, node)
	}
	return m.runSSHForcedCommand(ctx, task, run, rc, node)
}

func (m *Manager) sshRunPayload(task domain.Task, run domain.Run, rc runContext, node domain.ExecutorNode) ([]byte, error) {
	promptBytes, err := os.ReadFile(filepath.Join(rc.RunDir, "prompt.md"))
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"run_id":         run.ID,
		"task_id":        task.ID,
		"role":           run.Role,
		"prompt":         string(promptBytes),
		"forced_command": node.ForcedCommand,
	}
	return json.Marshal(payload)
}

func (m *Manager) runSSHAgentDHTTP(ctx context.Context, task domain.Task, run domain.Run, rc runContext, node domain.ExecutorNode) error {
	agentURL := strings.TrimRight(node.AgentDURL, "/")
	if agentURL == "" {
		return fmt.Errorf("ssh executor node %s has no agentd_url", node.ID)
	}

	payloadBytes, err := m.sshRunPayload(task, run, rc, node)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL+"/v1/runs", bytes.NewReader(payloadBytes))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if m.cfg.AgentDToken != "" {
		request.Header.Set("Authorization", "Bearer "+m.cfg.AgentDToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("agentd run request failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode >= 400 {
		return fmt.Errorf("agentd run request returned %s: %s", response.Status, redact(string(body)))
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("agentd result decode failed: %w", err)
	}
	if err := writeJSON(filepath.Join(rc.RunDir, "result.json"), result); err != nil {
		return err
	}
	if logs, err := m.fetchAgentDFile(ctx, agentURL, run.ID, "logs"); err == nil {
		if writeErr := os.WriteFile(filepath.Join(rc.RunDir, "worker.log"), logs, 0o644); writeErr != nil {
			return writeErr
		}
	} else {
		_ = os.WriteFile(filepath.Join(rc.RunDir, "worker.log"), []byte("agentd logs unavailable: "+redact(err.Error())+"\n"), 0o644)
	}
	if remoteResult, err := m.fetchAgentDFile(ctx, agentURL, run.ID, "result"); err == nil {
		_ = os.WriteFile(filepath.Join(rc.RunDir, "remote-result.json"), remoteResult, 0o644)
	}
	_ = os.WriteFile(filepath.Join(rc.RunDir, "diff.patch"), []byte(""), 0o644)

	_, _ = m.store.AddEvent(run.ID, "info", "ssh_agentd_run", "SSH executor collected agentd result and logs", map[string]any{
		"node_id":                   node.ID,
		"node_name":                 node.Name,
		"agentd_url":                agentURL,
		"host_key_fingerprint":      node.HostKeyFingerprint,
		"observed_host_fingerprint": node.ObservedHostKeyFingerprint,
		"forced_command":            node.ForcedCommand,
	})

	status := "succeeded"
	if resultStatus, ok := result["run_status"].(string); ok && resultStatus != "" {
		status = resultStatus
	} else if resultStatus, ok := result["status"].(string); ok && resultStatus == "blocked" {
		status = "blocked"
	}
	m.auditWorker(run.ID, "worker.ssh_agentd_run", "run", run.ID, map[string]any{
		"node_id":                   node.ID,
		"node_name":                 node.Name,
		"agentd_url":                agentURL,
		"host_key_fingerprint":      node.HostKeyFingerprint,
		"observed_host_fingerprint": node.ObservedHostKeyFingerprint,
		"forced_command":            node.ForcedCommand,
		"status":                    status,
	})
	_, err = m.store.FinishRun(run.ID, status, result)
	return err
}

func (m *Manager) runSSHForcedCommand(ctx context.Context, task domain.Task, run domain.Run, rc runContext, node domain.ExecutorNode) error {
	if strings.TrimSpace(node.Address) == "" {
		return fmt.Errorf("ssh executor node %s has no address", node.ID)
	}
	if strings.TrimSpace(node.ForcedCommand) == "" {
		return fmt.Errorf("ssh executor node %s has no forced_command", node.ID)
	}

	payloadBytes, err := m.sshRunPayload(task, run, rc, node)
	if err != nil {
		return err
	}
	args := sshCommandArgs(node, m.cfg)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = bytes.NewReader(payloadBytes)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	stderrText := redact(stderr.String())
	if err != nil {
		return fmt.Errorf("ssh forced command failed: %w: %s", err, stderrText)
	}
	if stdout.Len() == 0 {
		return fmt.Errorf("ssh forced command returned empty stdout: %s", stderrText)
	}

	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return fmt.Errorf("ssh forced command result decode failed: %w: %s", err, redact(stdout.String()))
	}
	if err := writeJSON(filepath.Join(rc.RunDir, "result.json"), result); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rc.RunDir, "remote-result.json"), stdout.Bytes(), 0o644); err != nil {
		return err
	}
	logText, _ := result["worker_log_content"].(string)
	if logText == "" {
		logText = "ssh forced command completed without worker_log_content\n"
	}
	if stderrText != "" {
		logText += "\n[ssh stderr]\n" + stderrText
		if !strings.HasSuffix(logText, "\n") {
			logText += "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(rc.RunDir, "worker.log"), []byte(logText), 0o644); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(rc.RunDir, "diff.patch"), []byte(""), 0o644)

	status := runStatusFromResult(result)
	_, _ = m.store.AddEvent(run.ID, "info", "ssh_forced_command_run", "SSH executor collected forced-command result", map[string]any{
		"node_id":                   node.ID,
		"node_name":                 node.Name,
		"address":                   node.Address,
		"host_key_fingerprint":      node.HostKeyFingerprint,
		"observed_host_fingerprint": node.ObservedHostKeyFingerprint,
		"forced_command":            node.ForcedCommand,
		"status":                    status,
	})
	m.auditWorker(run.ID, "worker.ssh_forced_command_run", "run", run.ID, map[string]any{
		"node_id":                   node.ID,
		"node_name":                 node.Name,
		"address":                   node.Address,
		"host_key_fingerprint":      node.HostKeyFingerprint,
		"observed_host_fingerprint": node.ObservedHostKeyFingerprint,
		"forced_command":            node.ForcedCommand,
		"status":                    status,
	})
	_, err = m.store.FinishRun(run.ID, status, result)
	return err
}

func runStatusFromResult(result map[string]any) string {
	status := "succeeded"
	if resultStatus, ok := result["run_status"].(string); ok && resultStatus != "" {
		return resultStatus
	}
	if resultStatus, ok := result["status"].(string); ok && resultStatus == "blocked" {
		return "blocked"
	}
	return status
}

func sshCommandArgs(node domain.ExecutorNode, cfg config.Config) []string {
	target, _, port := sshTargetHostPort(node.Address)
	args := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=" + fmt.Sprint(connectTimeoutSeconds(cfg.SSHConnectTimeout)),
	}
	if cfg.SSHPrivateKeyPath != "" {
		args = append(args, "-i", cfg.SSHPrivateKeyPath)
	}
	if cfg.SSHKnownHostsPath != "" {
		args = append(args, "-o", "UserKnownHostsFile="+cfg.SSHKnownHostsPath)
	}
	if port != "" {
		args = append(args, "-p", port)
	}
	args = append(args, target, node.ForcedCommand)
	return args
}

func sshTargetHostPort(address string) (target string, host string, port string) {
	port = "22"
	user := ""
	hostPort := strings.TrimSpace(address)
	if before, after, ok := strings.Cut(hostPort, "@"); ok {
		user = before
		hostPort = after
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(hostPort); err == nil {
		host = strings.Trim(parsedHost, "[]")
		port = parsedPort
	} else if strings.Count(hostPort, ":") == 1 {
		parsedHost, parsedPort, _ := strings.Cut(hostPort, ":")
		host = parsedHost
		if parsedPort != "" {
			port = parsedPort
		}
	} else {
		host = strings.Trim(hostPort, "[]")
	}
	target = host
	if user != "" {
		target = user + "@" + host
	}
	return target, host, port
}

func connectTimeoutSeconds(timeout time.Duration) int64 {
	if timeout <= 0 {
		return 15
	}
	seconds := int64(math.Ceil(timeout.Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (m *Manager) selectSSHNode() (domain.ExecutorNode, error) {
	for _, node := range m.store.ListExecutorNodes() {
		if node.Kind == "ssh" && node.Status == "active" && node.HostKeyVerified {
			return node, nil
		}
	}
	return domain.ExecutorNode{}, fmt.Errorf("no verified active ssh executor node is available")
}

func (m *Manager) fetchAgentDFile(ctx context.Context, agentURL string, runID string, kind string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, agentURL+"/v1/runs/"+runID+"/"+kind, nil)
	if err != nil {
		return nil, err
	}
	if m.cfg.AgentDToken != "" {
		request.Header.Set("Authorization", "Bearer "+m.cfg.AgentDToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("agentd %s returned %s: %s", kind, response.Status, redact(string(body)))
	}
	return body, nil
}

func (m *Manager) collectArtifactsAndScope(ctx context.Context, task domain.Task, run domain.Run, rc runContext) error {
	for _, item := range []struct {
		kind string
		name string
	}{
		{"worker_log", "worker.log"},
		{"result", "result.json"},
		{"remote_result", "remote-result.json"},
		{"diff", "diff.patch"},
	} {
		_, _ = m.recordArtifact(run.ID, rc.RunDir, item.kind, item.name)
	}

	changedFiles := gitChangedFiles(ctx, rc.Workspace)
	dependencyResult := policy.CheckDependencyPolicy(changedFiles, task.Envelope.Policy.AllowDependencyChange)
	dependencyPayload := map[string]any{
		"status":                  dependencyResult.Status,
		"allow_dependency_change": dependencyResult.AllowDependencyChange,
		"changed_files":           dependencyResult.ChangedFiles,
		"violations":              dependencyResult.Violations,
	}
	dependencyLevel := "info"
	dependencyMessage := "Dependency and lockfile policy passed"
	if dependencyResult.Status == "blocked" {
		dependencyLevel = "warn"
		dependencyMessage = "Dependency and lockfile policy blocked task"
	}
	_, _ = m.store.AddEvent(run.ID, dependencyLevel, "dependency_policy", dependencyMessage, dependencyPayload)
	m.auditWorker(run.ID, "worker.dependency_policy", "run", run.ID, dependencyPayload)
	if dependencyResult.Status == "blocked" {
		_, _ = m.store.UpdateTaskStatus(task.ID, "blocked")
	}
	if len(changedFiles) == 0 {
		return nil
	}
	result := policy.CheckScope(changedFiles, task.Envelope.AllowedPaths, task.Envelope.ForbiddenPaths)
	record, err := m.store.RecordScopeCheck(task.ID, run.ID, task.Envelope.BaseBranch, result)
	if err != nil {
		return err
	}
	_, _ = m.store.AddEvent(run.ID, "info", "scope_check", "Scope check recorded from git diff", map[string]any{
		"scope_check_id": record.ID,
		"status":         result.Status,
		"changed_files":  result.ChangedFiles,
		"violations":     result.Violations,
	})
	m.auditWorker(run.ID, "worker.scope_check", "run", run.ID, map[string]any{
		"scope_check_id": record.ID,
		"status":         result.Status,
		"changed_files":  result.ChangedFiles,
		"violations":     result.Violations,
	})
	if result.Status == "blocked" {
		_, _ = m.store.UpdateTaskStatus(task.ID, "blocked")
	}
	return nil
}

func (m *Manager) recordArtifact(runID string, runDir string, kind string, name string) (domain.Artifact, error) {
	path := filepath.Join(runDir, name)
	info, err := os.Stat(path)
	if err != nil {
		return domain.Artifact{}, err
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return domain.Artifact{}, err
	}
	return m.store.CreateArtifact(domain.Artifact{
		RunID:     runID,
		Kind:      kind,
		Name:      name,
		Path:      path,
		SHA256:    sum,
		SizeBytes: info.Size(),
		Metadata: map[string]any{
			"relative_path": name,
		},
	})
}

func (m *Manager) fail(task domain.Task, run domain.Run, err error) {
	_, _ = m.store.AddEvent(run.ID, "error", "executor_error", redact(err.Error()), nil)
	m.auditWorker(run.ID, "worker.executor_error", "run", run.ID, map[string]any{
		"error": redact(err.Error()),
	})
	_, _ = m.store.FinishRun(run.ID, "failed", map[string]any{
		"status": "failed",
		"error":  redact(err.Error()),
	})
	m.enqueueRetryIfAllowed(task, run, err)
}

func (m *Manager) enqueueRetryIfAllowed(task domain.Task, run domain.Run, err error) {
	maxAttempts := m.retryMaxAttempts(task)
	attempt := intFromResult(run.Result, "retry_attempt", 1)
	if maxAttempts <= 1 || attempt >= maxAttempts {
		return
	}
	nextAttempt := attempt + 1
	priority := m.queuePriority(task)
	queued, queueErr := m.store.EnqueueRun(task.ID, run.Role, run.Executor, priority, nextAttempt, maxAttempts, "retry_after_failure")
	payload := map[string]any{
		"failed_run_id": run.ID,
		"attempt":       nextAttempt,
		"max_attempts":  maxAttempts,
		"priority":      priority,
		"reason":        "retry_after_failure",
	}
	if queueErr != nil {
		payload["error"] = redact(queueErr.Error())
		_, _ = m.store.AddEvent(run.ID, "warn", "worker_retry_enqueue_failed", "Worker retry enqueue failed", payload)
		m.auditWorker(run.ID, "worker.retry_enqueue_failed", "run", run.ID, payload)
		return
	}
	payload["queued_run_id"] = queued.ID
	_, _ = m.store.AddEvent(run.ID, "info", "worker_retry_queued", "Worker retry was queued", payload)
	m.auditWorker(run.ID, "worker.retry_queued", "run", queued.ID, payload)
}

func (m *Manager) retryMaxAttempts(task domain.Task) int {
	for _, profile := range m.store.ListAgentProfiles(task.ProjectID) {
		if profile.Name != task.Envelope.AgentProfile && profile.ID != task.Envelope.AgentProfile {
			continue
		}
		if value := intFromConfigMap(profile.Config, "retry_max_attempts", 0); value > 0 {
			return value
		}
		if retry, ok := profile.Config["retry"].(map[string]any); ok {
			if value := intFromConfigMap(retry, "max_attempts", 0); value > 0 {
				return value
			}
		}
	}
	return 1
}

func (m *Manager) queuePriority(task domain.Task) int {
	for _, profile := range m.store.ListAgentProfiles(task.ProjectID) {
		if profile.Name == task.Envelope.AgentProfile || profile.ID == task.Envelope.AgentProfile {
			return intFromConfigMap(profile.Config, "queue_priority", 0)
		}
	}
	return 0
}

func (m *Manager) timeout(runID string, timeout time.Duration) {
	payload := timeoutPayload(timeout)
	payload["status"] = "timed_out"
	_, _ = m.store.AddEvent(runID, "error", "executor_timeout", "Worker exceeded timeout and was stopped", payload)
	m.auditWorker(runID, "worker.executor_timeout", "run", runID, payload)
	_, _ = m.store.FinishRun(runID, "timed_out", payload)
}

func (m *Manager) cleanupDockerContainer(runID string, containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerName).CombinedOutput()
	payload := map[string]any{"container": containerName, "output": redact(string(output))}
	if err != nil {
		payload["error"] = redact(err.Error())
		_, _ = m.store.AddEvent(runID, "warn", "docker_timeout_cleanup_failed", "Docker timeout cleanup failed", payload)
		return
	}
	_, _ = m.store.AddEvent(runID, "info", "docker_timeout_cleanup", "Docker timeout cleanup completed", payload)
}

func (m *Manager) runTimeout(task domain.Task) time.Duration {
	for _, profile := range m.store.ListAgentProfiles(task.ProjectID) {
		if profile.Name != task.Envelope.AgentProfile && profile.ID != task.Envelope.AgentProfile {
			continue
		}
		if timeout := timeoutFromConfig(profile.Config); timeout > 0 {
			return timeout
		}
	}
	return m.cfg.WorkerDefaultTimeout
}

func timeoutFromConfig(cfg map[string]any) time.Duration {
	if cfg == nil {
		return 0
	}
	value, ok := cfg["timeout_seconds"]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return time.Duration(typed) * time.Second
		}
	case int64:
		if typed > 0 {
			return time.Duration(typed) * time.Second
		}
	case float64:
		if typed > 0 {
			return time.Duration(typed) * time.Second
		}
	case json.Number:
		seconds, err := typed.Int64()
		if err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 0
}

func intFromResult(values map[string]any, key string, fallback int) int {
	return intFromConfigMap(values, key, fallback)
}

func mergeConfigMaps(base map[string]any, override map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func stringFromConfigMap(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			return strings.TrimSpace(typed)
		case json.Number:
			return typed.String()
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		}
	}
	return ""
}

func intFromConfigMapAnyKey(values map[string]any, fallback int, keys ...string) int {
	for _, key := range keys {
		if value := intFromConfigMap(values, key, 0); value > 0 {
			return value
		}
	}
	return fallback
}

func intFromConfigMap(values map[string]any, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func boolFromConfigMapAnyKey(values map[string]any, keys ...string) (bool, bool) {
	if values == nil {
		return false, false
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
			if err == nil {
				return parsed, true
			}
		}
	}
	return false, false
}

func timeoutPayload(timeout time.Duration) map[string]any {
	return map[string]any{
		"timeout":         timeout.String(),
		"timeout_seconds": timeout.Seconds(),
	}
}

func renderPrompt(task domain.Task, run domain.Run) string {
	envelope, _ := json.MarshalIndent(task.Envelope, "", "  ")
	return fmt.Sprintf(`# multi-codex worker task

Role: %s
Task: %s

You are running inside a controlled worker environment. Follow AGENTS.md and the task envelope exactly.

Required security boundaries:
- Write only inside allowed_paths.
- Stop before touching forbidden_paths.
- Use only allowed_commands unless the task requires a human decision.
- Never push, merge, or read secrets.

Task Envelope:

%s
`, run.Role, task.TaskKey, string(envelope))
}

func renderAgentOverride(task domain.Task, run domain.Run) string {
	return fmt.Sprintf(`# multi-codex worker override

Task ID: %s
Run ID: %s
Role: %s

Allowed paths:
%s

Forbidden paths:
%s
`, task.TaskKey, run.ID, run.Role, bulletList(task.Envelope.AllowedPaths), bulletList(task.Envelope.ForbiddenPaths))
}

func dockerWorkerCommand(task domain.Task, run domain.Run) string {
	taskJSON, _ := json.Marshal(task)
	taskArg := strings.ReplaceAll(string(taskJSON), "'", "'\"'\"'")
	return fmt.Sprintf(`set -euo pipefail
echo "docker executor started role=%s task=%s" > /runs/worker.log
if command -v codex >/dev/null 2>&1; then
  codex exec --skip-git-repo-check < /runs/prompt.md >> /runs/worker.log 2>&1 || worker_status=$?
else
  echo "codex CLI not found in worker image; install Codex in the explicit worker image build to execute AI work" >> /runs/worker.log
  worker_status=77
fi
git -C /workspace diff --binary > /runs/diff.patch 2>> /runs/worker.log || true
changed_files="$(git -C /workspace diff --name-only 2>> /runs/worker.log | jq -R . | jq -s . || echo '[]')"
if [[ "${worker_status:-0}" == "0" ]]; then run_status="succeeded"; status="done"; else run_status="blocked"; status="blocked"; fi
jq -n --arg status "$status" --arg run_status "$run_status" --arg task_id "%s" --arg task_key "%s" --arg role "%s" --arg summary "Docker executor completed worker process" --argjson changed_files "$changed_files" '{status:$status,run_status:$run_status,task_id:$task_id,task_key:$task_key,role:$role,summary:$summary,changed_files:$changed_files,tests_run:[],tests_failed:[],risks:[],needs_human:(if $run_status == "blocked" then ["Codex CLI was not available or worker exited non-zero"] else [] end)}' > /runs/result.json
`, run.Role, task.TaskKey, task.ID, task.TaskKey, run.Role) + "\n# task snapshot: '" + taskArg + "'\n"
}

func localSummary(role string) string {
	switch role {
	case "test":
		return "Local test worker completed required command collection in dry-run mode."
	case "audit":
		return "Local audit worker completed read-only audit lifecycle with no blockers."
	case "git_sync":
		return "Local Git Sync worker completed PR preparation lifecycle."
	default:
		return "Local executor completed the worker lifecycle without invoking an AI worker."
	}
}

func testsForRole(task domain.Task, role string) []string {
	if role == "test" {
		return append([]string(nil), task.Envelope.AllowedCommands...)
	}
	return []string{}
}

func localRisks(role string) []string {
	if role == "feature" {
		return []string{"Local executor does not modify repository content; use docker executor with the fixed Codex worker image for AI implementation."}
	}
	return []string{}
}

func writeDiff(ctx context.Context, workspace string, path string) error {
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		return os.WriteFile(path, []byte(""), 0o644)
	}
	output, err := exec.CommandContext(ctx, "git", "-C", workspace, "diff", "--binary").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git diff failed: %w: %s", err, redact(string(output)))
	}
	return os.WriteFile(path, output, 0o644)
}

func gitChangedFiles(ctx context.Context, workspace string) []string {
	if _, err := os.Stat(filepath.Join(workspace, ".git")); err != nil {
		return []string{}
	}
	output, err := exec.CommandContext(ctx, "git", "-C", workspace, "diff", "--name-only").CombinedOutput()
	if err != nil {
		return []string{}
	}
	var files []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func bulletList(values []string) string {
	if len(values) == 0 {
		return "- none"
	}
	var b strings.Builder
	for _, value := range values {
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func safePathComponent(value string) string {
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, ":", "_")
	return value
}

var structuredSecretDetectors = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bsk-[a-z0-9][a-z0-9_-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{20,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	regexp.MustCompile(`(?i)\b(secret|token|password|credential|api[_-]?key)\b\s*[:=]\s*["']?[^ \n\r\t"']+`),
}

func redact(value string) string {
	replacements := []string{"secret", "token", "password", "credential", "api_key"}
	redacted := value
	for _, detector := range structuredSecretDetectors {
		redacted = detector.ReplaceAllString(redacted, "[redacted-secret]")
	}
	for _, needle := range replacements {
		redacted = strings.ReplaceAll(redacted, needle, "[redacted-keyword]")
		redacted = strings.ReplaceAll(redacted, strings.ToUpper(needle), "[redacted-keyword]")
	}
	return redacted
}

func redactWithSecrets(value string, secretValues []string) string {
	redacted := redact(value)
	for _, secret := range secretValues {
		if len(secret) < 4 {
			continue
		}
		redacted = strings.ReplaceAll(redacted, secret, "[redacted-secret]")
	}
	return redacted
}
