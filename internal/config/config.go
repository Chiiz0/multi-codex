package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	authn "github.com/Chiiz0/multi-codex/internal/auth"
)

type Config struct {
	APIListen                        string
	MCPListen                        string
	AgentDListen                     string
	AgentDURL                        string
	AgentDToken                      string
	MCPSessionTTL                    time.Duration
	MCPLiveFanoutInterval            time.Duration
	DatabaseURL                      string
	ArtifactRoot                     string
	RunRoot                          string
	WorktreeRoot                     string
	RepoCacheRoot                    string
	ExecutorMode                     string
	WorkerImage                      string
	WorkerDefaultTimeout             time.Duration
	WorkerSecretEnvAllowlist         []string
	WorkerSecretProvider             string
	WorkerSecretFilePath             string
	WorkerVaultAddress               string
	WorkerVaultToken                 string
	WorkerVaultTokenFile             string
	WorkerVaultNamespace             string
	WorkerVaultMount                 string
	WorkerVaultSecretPath            string
	SSHPrivateKeyPath                string
	SSHKnownHostsPath                string
	SSHConnectTimeout                time.Duration
	AuthMode                         string
	AuthSessionTTL                   time.Duration
	AuthCookieSecure                 bool
	AuthLoginStateTTL                time.Duration
	OIDCIssuer                       string
	OIDCAudience                     string
	OIDCJWKSURL                      string
	OIDCClientID                     string
	OIDCClientSecret                 string
	OIDCClientAuthMethod             string
	OIDCRedirectURL                  string
	OIDCAuthorizationURL             string
	OIDCTokenURL                     string
	OIDCPostLoginRedirectURL         string
	OIDCDefaultRole                  string
	OIDCDefaultOrgID                 string
	OIDCGroupRoleMap                 []authn.ClaimMapping
	OIDCGroupOrgMap                  []authn.ClaimMapping
	RetentionEnabled                 bool
	RetentionInterval                time.Duration
	RetentionMaxAge                  time.Duration
	RetentionDryRun                  bool
	QueueEnabled                     bool
	QueueDispatchInterval            time.Duration
	TelemetryPushURL                 string
	TelemetryPushInterval            time.Duration
	AuditShipEnabled                 bool
	AuditShipInterval                time.Duration
	AuditSealRoot                    string
	AuditShipTarget                  string
	AuditShipAllowLegacyHashMismatch bool
	GitSyncMode                      string
	GitCredentialProvider            string
	GitCredentialFilePath            string
	GitVaultAddress                  string
	GitVaultToken                    string
	GitVaultTokenFile                string
	GitVaultNamespace                string
	GitVaultMount                    string
	GitVaultSecretPath               string
	GitHubToken                      string
	GitHubAPIURL                     string
	GitLabToken                      string
	GitLabAPIURL                     string
}

func FromEnv() Config {
	return Config{
		APIListen:                        env("MULTICODEX_API_LISTEN", ":8080"),
		MCPListen:                        env("MULTICODEX_MCP_LISTEN", ":8090"),
		AgentDListen:                     env("MULTICODEX_AGENTD_LISTEN", ":7070"),
		AgentDURL:                        env("MULTICODEX_AGENTD_URL", "http://localhost:7070"),
		AgentDToken:                      env("MULTICODEX_AGENTD_TOKEN", ""),
		MCPSessionTTL:                    envDuration("MULTICODEX_MCP_SESSION_TTL", 8*time.Hour),
		MCPLiveFanoutInterval:            envDuration("MULTICODEX_MCP_LIVE_FANOUT_INTERVAL", time.Second),
		DatabaseURL:                      env("MULTICODEX_DATABASE_URL", ""),
		ArtifactRoot:                     env("MULTICODEX_ARTIFACT_ROOT", "./.data/artifacts"),
		RunRoot:                          env("MULTICODEX_RUN_ROOT", "./.data/runs"),
		WorktreeRoot:                     env("MULTICODEX_WORKTREE_ROOT", "./.data/worktrees"),
		RepoCacheRoot:                    env("MULTICODEX_REPO_CACHE_ROOT", "./.data/repos"),
		ExecutorMode:                     env("MULTICODEX_EXECUTOR_MODE", "mock"),
		WorkerImage:                      env("MULTICODEX_WORKER_IMAGE", "multi-codex/codex-worker:go1.25-node-vite8"),
		WorkerDefaultTimeout:             envDuration("MULTICODEX_WORKER_DEFAULT_TIMEOUT", time.Hour),
		WorkerSecretEnvAllowlist:         envList("MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST"),
		WorkerSecretProvider:             env("MULTICODEX_WORKER_SECRET_PROVIDER", "env"),
		WorkerSecretFilePath:             env("MULTICODEX_WORKER_SECRET_FILE_PATH", ""),
		WorkerVaultAddress:               env("MULTICODEX_WORKER_VAULT_ADDR", ""),
		WorkerVaultToken:                 env("MULTICODEX_WORKER_VAULT_TOKEN", ""),
		WorkerVaultTokenFile:             env("MULTICODEX_WORKER_VAULT_TOKEN_FILE", ""),
		WorkerVaultNamespace:             env("MULTICODEX_WORKER_VAULT_NAMESPACE", ""),
		WorkerVaultMount:                 env("MULTICODEX_WORKER_VAULT_MOUNT", "secret"),
		WorkerVaultSecretPath:            env("MULTICODEX_WORKER_VAULT_SECRET_PATH", ""),
		SSHPrivateKeyPath:                env("MULTICODEX_SSH_PRIVATE_KEY_PATH", ""),
		SSHKnownHostsPath:                env("MULTICODEX_SSH_KNOWN_HOSTS_PATH", ""),
		SSHConnectTimeout:                envDuration("MULTICODEX_SSH_CONNECT_TIMEOUT", 15*time.Second),
		AuthMode:                         env("MULTICODEX_AUTH_MODE", "local"),
		AuthSessionTTL:                   envDuration("MULTICODEX_AUTH_SESSION_TTL", 12*time.Hour),
		AuthCookieSecure:                 envBool("MULTICODEX_AUTH_COOKIE_SECURE", false),
		AuthLoginStateTTL:                envDuration("MULTICODEX_AUTH_LOGIN_STATE_TTL", 10*time.Minute),
		OIDCIssuer:                       env("MULTICODEX_OIDC_ISSUER", ""),
		OIDCAudience:                     env("MULTICODEX_OIDC_AUDIENCE", ""),
		OIDCJWKSURL:                      env("MULTICODEX_OIDC_JWKS_URL", ""),
		OIDCClientID:                     env("MULTICODEX_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:                 env("MULTICODEX_OIDC_CLIENT_SECRET", ""),
		OIDCClientAuthMethod:             env("MULTICODEX_OIDC_CLIENT_AUTH_METHOD", "client_secret_post"),
		OIDCRedirectURL:                  env("MULTICODEX_OIDC_REDIRECT_URL", ""),
		OIDCAuthorizationURL:             env("MULTICODEX_OIDC_AUTHORIZATION_URL", ""),
		OIDCTokenURL:                     env("MULTICODEX_OIDC_TOKEN_URL", ""),
		OIDCPostLoginRedirectURL:         env("MULTICODEX_OIDC_POST_LOGIN_REDIRECT_URL", "/"),
		OIDCDefaultRole:                  env("MULTICODEX_OIDC_DEFAULT_ROLE", "viewer"),
		OIDCDefaultOrgID:                 env("MULTICODEX_OIDC_DEFAULT_ORG_ID", ""),
		OIDCGroupRoleMap:                 envClaimMappings("MULTICODEX_OIDC_GROUP_ROLE_MAP"),
		OIDCGroupOrgMap:                  envClaimMappings("MULTICODEX_OIDC_GROUP_ORG_MAP"),
		RetentionEnabled:                 envBool("MULTICODEX_RETENTION_ENABLED", false),
		RetentionInterval:                envDuration("MULTICODEX_RETENTION_INTERVAL", time.Hour),
		RetentionMaxAge:                  envDuration("MULTICODEX_RETENTION_MAX_AGE", 720*time.Hour),
		RetentionDryRun:                  envBool("MULTICODEX_RETENTION_DRY_RUN", true),
		QueueEnabled:                     envBool("MULTICODEX_QUEUE_ENABLED", true),
		QueueDispatchInterval:            envDuration("MULTICODEX_QUEUE_DISPATCH_INTERVAL", 5*time.Second),
		TelemetryPushURL:                 env("MULTICODEX_TELEMETRY_PUSH_URL", ""),
		TelemetryPushInterval:            envDuration("MULTICODEX_TELEMETRY_PUSH_INTERVAL", time.Minute),
		AuditShipEnabled:                 envBool("MULTICODEX_AUDIT_SHIP_ENABLED", false),
		AuditShipInterval:                envDuration("MULTICODEX_AUDIT_SHIP_INTERVAL", 24*time.Hour),
		AuditSealRoot:                    env("MULTICODEX_AUDIT_SEAL_ROOT", "./.data/audit-seals/scheduled"),
		AuditShipTarget:                  env("MULTICODEX_AUDIT_SHIP_TARGET", ""),
		AuditShipAllowLegacyHashMismatch: envBool("MULTICODEX_AUDIT_SHIP_ALLOW_LEGACY_HASH_MISMATCH", false),
		GitSyncMode:                      env("MULTICODEX_GIT_SYNC_MODE", "dry-run"),
		GitCredentialProvider:            env("MULTICODEX_GIT_CREDENTIAL_PROVIDER", "env"),
		GitCredentialFilePath:            env("MULTICODEX_GIT_CREDENTIAL_FILE_PATH", ""),
		GitVaultAddress:                  env("MULTICODEX_GIT_VAULT_ADDR", ""),
		GitVaultToken:                    env("MULTICODEX_GIT_VAULT_TOKEN", ""),
		GitVaultTokenFile:                env("MULTICODEX_GIT_VAULT_TOKEN_FILE", ""),
		GitVaultNamespace:                env("MULTICODEX_GIT_VAULT_NAMESPACE", ""),
		GitVaultMount:                    env("MULTICODEX_GIT_VAULT_MOUNT", "secret"),
		GitVaultSecretPath:               env("MULTICODEX_GIT_VAULT_SECRET_PATH", ""),
		GitHubToken:                      env("GITHUB_TOKEN", ""),
		GitHubAPIURL:                     env("MULTICODEX_GITHUB_API_URL", "https://api.github.com"),
		GitLabToken:                      env("GITLAB_TOKEN", ""),
		GitLabAPIURL:                     env("MULTICODEX_GITLAB_API_URL", "https://gitlab.com/api/v4"),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(key string) []string {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t' || r == ' '
	})
	values := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		values = append(values, part)
		seen[part] = true
	}
	return values
}

func envClaimMappings(key string) []authn.ClaimMapping {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	mappings := make([]authn.ClaimMapping, 0, len(parts))
	for _, part := range parts {
		claim, mapped, ok := strings.Cut(part, "=")
		if !ok {
			claim, mapped, ok = strings.Cut(part, ":")
		}
		if !ok {
			continue
		}
		claim = strings.TrimSpace(claim)
		mapped = strings.TrimSpace(mapped)
		if claim == "" || mapped == "" {
			continue
		}
		mappings = append(mappings, authn.ClaimMapping{Claim: claim, Value: mapped})
	}
	return mappings
}
