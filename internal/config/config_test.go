package config

import (
	"testing"
	"time"
)

func TestFromEnvParsesRetentionConfig(t *testing.T) {
	t.Setenv("MULTICODEX_RETENTION_ENABLED", "true")
	t.Setenv("MULTICODEX_RETENTION_INTERVAL", "30m")
	t.Setenv("MULTICODEX_RETENTION_MAX_AGE", "48h")
	t.Setenv("MULTICODEX_RETENTION_DRY_RUN", "false")
	t.Setenv("MULTICODEX_WORKER_DEFAULT_TIMEOUT", "15m")
	t.Setenv("MULTICODEX_SSH_CONNECT_TIMEOUT", "7s")
	t.Setenv("MULTICODEX_SSH_PRIVATE_KEY_PATH", "/run/secrets/multi-codex-ssh")
	t.Setenv("MULTICODEX_SSH_KNOWN_HOSTS_PATH", "/run/secrets/known_hosts")
	t.Setenv("MULTICODEX_QUEUE_ENABLED", "false")
	t.Setenv("MULTICODEX_QUEUE_DISPATCH_INTERVAL", "3s")
	t.Setenv("MULTICODEX_TELEMETRY_PUSH_URL", "http://collector.example/v1/metrics")
	t.Setenv("MULTICODEX_TELEMETRY_PUSH_INTERVAL", "45s")
	t.Setenv("MULTICODEX_MCP_SESSION_TTL", "30m")
	t.Setenv("MULTICODEX_MCP_LIVE_FANOUT_INTERVAL", "250ms")
	t.Setenv("MULTICODEX_AGENTD_TOKEN", "agentd-secret")
	t.Setenv("MULTICODEX_AUTH_SESSION_TTL", "6h")
	t.Setenv("MULTICODEX_AUTH_COOKIE_SECURE", "true")
	t.Setenv("MULTICODEX_AUTH_LOGIN_STATE_TTL", "2m")
	t.Setenv("MULTICODEX_OIDC_CLIENT_ID", "multi-codex-web")
	t.Setenv("MULTICODEX_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("MULTICODEX_OIDC_CLIENT_AUTH_METHOD", "client_secret_basic")
	t.Setenv("MULTICODEX_OIDC_REDIRECT_URL", "https://multi-codex.example/api/v1/auth/callback")
	t.Setenv("MULTICODEX_OIDC_AUTHORIZATION_URL", "https://issuer.example/oauth2/auth")
	t.Setenv("MULTICODEX_OIDC_TOKEN_URL", "https://issuer.example/oauth2/token")
	t.Setenv("MULTICODEX_OIDC_POST_LOGIN_REDIRECT_URL", "/#dashboard")
	t.Setenv("MULTICODEX_AUDIT_SHIP_ENABLED", "true")
	t.Setenv("MULTICODEX_AUDIT_SHIP_INTERVAL", "2h")
	t.Setenv("MULTICODEX_AUDIT_SEAL_ROOT", "/var/lib/multi-codex/audit-seals")
	t.Setenv("MULTICODEX_AUDIT_SHIP_TARGET", "file:///worm/multi-codex")
	t.Setenv("MULTICODEX_AUDIT_SHIP_ALLOW_LEGACY_HASH_MISMATCH", "true")

	cfg := FromEnv()
	if !cfg.RetentionEnabled {
		t.Fatalf("retention should be enabled")
	}
	if cfg.RetentionInterval != 30*time.Minute {
		t.Fatalf("retention interval = %s", cfg.RetentionInterval)
	}
	if cfg.RetentionMaxAge != 48*time.Hour {
		t.Fatalf("retention max age = %s", cfg.RetentionMaxAge)
	}
	if cfg.RetentionDryRun {
		t.Fatalf("retention dry run should be false")
	}
	if cfg.WorkerDefaultTimeout != 15*time.Minute {
		t.Fatalf("worker default timeout = %s", cfg.WorkerDefaultTimeout)
	}
	if cfg.SSHConnectTimeout != 7*time.Second {
		t.Fatalf("ssh connect timeout = %s", cfg.SSHConnectTimeout)
	}
	if cfg.SSHPrivateKeyPath != "/run/secrets/multi-codex-ssh" {
		t.Fatalf("ssh key path = %q", cfg.SSHPrivateKeyPath)
	}
	if cfg.SSHKnownHostsPath != "/run/secrets/known_hosts" {
		t.Fatalf("ssh known hosts path = %q", cfg.SSHKnownHostsPath)
	}
	if cfg.QueueEnabled {
		t.Fatalf("queue should be disabled")
	}
	if cfg.QueueDispatchInterval != 3*time.Second {
		t.Fatalf("queue interval = %s", cfg.QueueDispatchInterval)
	}
	if cfg.TelemetryPushURL != "http://collector.example/v1/metrics" {
		t.Fatalf("telemetry push url = %q", cfg.TelemetryPushURL)
	}
	if cfg.TelemetryPushInterval != 45*time.Second {
		t.Fatalf("telemetry push interval = %s", cfg.TelemetryPushInterval)
	}
	if cfg.MCPSessionTTL != 30*time.Minute {
		t.Fatalf("mcp session ttl = %s", cfg.MCPSessionTTL)
	}
	if cfg.MCPLiveFanoutInterval != 250*time.Millisecond {
		t.Fatalf("mcp live fanout interval = %s", cfg.MCPLiveFanoutInterval)
	}
	if cfg.AgentDToken != "agentd-secret" {
		t.Fatalf("agentd token = %q", cfg.AgentDToken)
	}
	if cfg.AuthSessionTTL != 6*time.Hour {
		t.Fatalf("auth session ttl = %s", cfg.AuthSessionTTL)
	}
	if !cfg.AuthCookieSecure {
		t.Fatalf("auth cookie secure should be enabled")
	}
	if cfg.AuthLoginStateTTL != 2*time.Minute {
		t.Fatalf("auth login state ttl = %s", cfg.AuthLoginStateTTL)
	}
	if cfg.OIDCClientID != "multi-codex-web" || cfg.OIDCClientSecret != "client-secret" {
		t.Fatalf("oidc client config = %#v", cfg)
	}
	if cfg.OIDCClientAuthMethod != "client_secret_basic" {
		t.Fatalf("oidc client auth method = %q", cfg.OIDCClientAuthMethod)
	}
	if cfg.OIDCRedirectURL != "https://multi-codex.example/api/v1/auth/callback" {
		t.Fatalf("oidc redirect url = %q", cfg.OIDCRedirectURL)
	}
	if cfg.OIDCAuthorizationURL != "https://issuer.example/oauth2/auth" || cfg.OIDCTokenURL != "https://issuer.example/oauth2/token" {
		t.Fatalf("oidc endpoint config = %#v", cfg)
	}
	if cfg.OIDCPostLoginRedirectURL != "/#dashboard" {
		t.Fatalf("post login redirect = %q", cfg.OIDCPostLoginRedirectURL)
	}
	if cfg.AuditShipTarget != "file:///worm/multi-codex" {
		t.Fatalf("audit ship target = %q", cfg.AuditShipTarget)
	}
	if !cfg.AuditShipEnabled {
		t.Fatalf("audit ship should be enabled")
	}
	if cfg.AuditShipInterval != 2*time.Hour {
		t.Fatalf("audit ship interval = %s", cfg.AuditShipInterval)
	}
	if cfg.AuditSealRoot != "/var/lib/multi-codex/audit-seals" {
		t.Fatalf("audit seal root = %q", cfg.AuditSealRoot)
	}
	if !cfg.AuditShipAllowLegacyHashMismatch {
		t.Fatalf("audit ship legacy mismatch compatibility should be enabled")
	}
}

func TestFromEnvParsesOIDCClaimMappings(t *testing.T) {
	t.Setenv("MULTICODEX_OIDC_DEFAULT_ORG_ID", "00000000-0000-7000-8000-000000000001")
	t.Setenv("MULTICODEX_OIDC_GROUP_ROLE_MAP", "engineering=operator; auditors:auditor, invalid")
	t.Setenv("MULTICODEX_OIDC_GROUP_ORG_MAP", "engineering=00000000-0000-7000-8000-000000000099")

	cfg := FromEnv()
	if cfg.OIDCDefaultOrgID != "00000000-0000-7000-8000-000000000001" {
		t.Fatalf("default org id = %q", cfg.OIDCDefaultOrgID)
	}
	if len(cfg.OIDCGroupRoleMap) != 2 {
		t.Fatalf("role map length = %d", len(cfg.OIDCGroupRoleMap))
	}
	if cfg.OIDCGroupRoleMap[0].Claim != "engineering" || cfg.OIDCGroupRoleMap[0].Value != "operator" {
		t.Fatalf("first role mapping = %#v", cfg.OIDCGroupRoleMap[0])
	}
	if cfg.OIDCGroupRoleMap[1].Claim != "auditors" || cfg.OIDCGroupRoleMap[1].Value != "auditor" {
		t.Fatalf("second role mapping = %#v", cfg.OIDCGroupRoleMap[1])
	}
	if len(cfg.OIDCGroupOrgMap) != 1 || cfg.OIDCGroupOrgMap[0].Claim != "engineering" {
		t.Fatalf("org mappings = %#v", cfg.OIDCGroupOrgMap)
	}
}

func TestFromEnvParsesWorkerSecretEnvAllowlist(t *testing.T) {
	t.Setenv("MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST", "OPENAI_API_KEY,CODEX_AUTH_TOKEN; GITHUB_TOKEN\nOPENAI_API_KEY")
	t.Setenv("MULTICODEX_WORKER_SECRET_PROVIDER", "vault")
	t.Setenv("MULTICODEX_WORKER_SECRET_FILE_PATH", "/run/secrets/multi-codex-worker.json")
	t.Setenv("MULTICODEX_WORKER_VAULT_ADDR", "https://vault.example")
	t.Setenv("MULTICODEX_WORKER_VAULT_TOKEN", "vault-token")
	t.Setenv("MULTICODEX_WORKER_VAULT_TOKEN_FILE", "/run/secrets/vault-token")
	t.Setenv("MULTICODEX_WORKER_VAULT_NAMESPACE", "engineering")
	t.Setenv("MULTICODEX_WORKER_VAULT_MOUNT", "kv")
	t.Setenv("MULTICODEX_WORKER_VAULT_SECRET_PATH", "multi-codex/worker")

	cfg := FromEnv()
	want := []string{"OPENAI_API_KEY", "CODEX_AUTH_TOKEN", "GITHUB_TOKEN"}
	if len(cfg.WorkerSecretEnvAllowlist) != len(want) {
		t.Fatalf("allowlist length = %d: %#v", len(cfg.WorkerSecretEnvAllowlist), cfg.WorkerSecretEnvAllowlist)
	}
	for i := range want {
		if cfg.WorkerSecretEnvAllowlist[i] != want[i] {
			t.Fatalf("allowlist[%d] = %q, want %q", i, cfg.WorkerSecretEnvAllowlist[i], want[i])
		}
	}
	if cfg.WorkerSecretProvider != "vault" {
		t.Fatalf("secret provider = %q", cfg.WorkerSecretProvider)
	}
	if cfg.WorkerSecretFilePath != "/run/secrets/multi-codex-worker.json" {
		t.Fatalf("secret file path = %q", cfg.WorkerSecretFilePath)
	}
	if cfg.WorkerVaultAddress != "https://vault.example" || cfg.WorkerVaultToken != "vault-token" || cfg.WorkerVaultTokenFile != "/run/secrets/vault-token" {
		t.Fatalf("vault connection config = %#v", cfg)
	}
	if cfg.WorkerVaultNamespace != "engineering" || cfg.WorkerVaultMount != "kv" || cfg.WorkerVaultSecretPath != "multi-codex/worker" {
		t.Fatalf("vault path config = %#v", cfg)
	}
}

func TestFromEnvParsesGitCredentialResolverConfig(t *testing.T) {
	t.Setenv("MULTICODEX_GIT_CREDENTIAL_PROVIDER", "vault")
	t.Setenv("MULTICODEX_GIT_CREDENTIAL_FILE_PATH", "/run/secrets/git-provider.json")
	t.Setenv("MULTICODEX_GIT_VAULT_ADDR", "https://vault.example")
	t.Setenv("MULTICODEX_GIT_VAULT_TOKEN", "git-vault-token")
	t.Setenv("MULTICODEX_GIT_VAULT_TOKEN_FILE", "/run/secrets/git-vault-token")
	t.Setenv("MULTICODEX_GIT_VAULT_NAMESPACE", "engineering")
	t.Setenv("MULTICODEX_GIT_VAULT_MOUNT", "kv")
	t.Setenv("MULTICODEX_GIT_VAULT_SECRET_PATH", "multi-codex/git")

	cfg := FromEnv()
	if cfg.GitCredentialProvider != "vault" || cfg.GitCredentialFilePath != "/run/secrets/git-provider.json" {
		t.Fatalf("git credential config = %#v", cfg)
	}
	if cfg.GitVaultAddress != "https://vault.example" || cfg.GitVaultToken != "git-vault-token" || cfg.GitVaultTokenFile != "/run/secrets/git-vault-token" {
		t.Fatalf("git vault connection config = %#v", cfg)
	}
	if cfg.GitVaultNamespace != "engineering" || cfg.GitVaultMount != "kv" || cfg.GitVaultSecretPath != "multi-codex/git" {
		t.Fatalf("git vault path config = %#v", cfg)
	}
}
