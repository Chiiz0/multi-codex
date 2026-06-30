package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateProductionAllowsLocalDefaultsOutsideProduction(t *testing.T) {
	cfg := Config{
		AuthMode:         "local",
		AuthCookieSecure: false,
	}
	if err := ValidateProduction(cfg, "api"); err != nil {
		t.Fatalf("local defaults should be allowed outside production: %v", err)
	}
}

func TestValidateProductionRejectsUnsafeAPIConfig(t *testing.T) {
	cfg := Config{
		Environment:        "production",
		AuthMode:           "local",
		DatabaseURL:        "postgres://multi_codex@postgres:5432/multi_codex?sslmode=disable",
		CORSAllowedOrigins: []string{"*"},
		AgentDURL:          "http://worker-agentd:7070",
		AuditSealRoot:      "/var/lib/multi-codex/audit-seals",
	}
	err := ValidateProduction(cfg, "api")
	if err == nil {
		t.Fatalf("expected unsafe production config to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"MULTICODEX_AUTH_MODE must be oidc",
		"MULTICODEX_AUTH_COOKIE_SECURE must be true",
		"MULTICODEX_OIDC_ISSUER is required",
		"MULTICODEX_DATABASE_URL must include a non-empty password",
		"MULTICODEX_CORS_ALLOWED_ORIGINS cannot contain wildcard origins",
		"MULTICODEX_AGENTD_TOKEN is required",
		"MULTICODEX_WORKER_DEFAULT_TIMEOUT must be positive",
		"MULTICODEX_WORKER_READ_ONLY_ROOTFS must be true",
		"MULTICODEX_RETENTION_ENABLED must be true",
		"MULTICODEX_AUDIT_SHIP_ENABLED must be true",
		"MULTICODEX_AUDIT_SHIP_TARGET is required",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
}

func TestValidateProductionAcceptsSafeAPIConfig(t *testing.T) {
	cfg := strictProductionConfig()
	if err := ValidateProduction(cfg, "api"); err != nil {
		t.Fatalf("strict production config should pass: %v", err)
	}
}

func TestValidateProductionRejectsUnsafeMCPConfig(t *testing.T) {
	cfg := strictProductionConfig()
	cfg.OIDCJWKSURL = ""
	cfg.DatabaseURL = "postgres://multi_codex@postgres:5432/multi_codex?sslmode=disable"
	err := ValidateProduction(cfg, "mcp-gateway")
	if err == nil {
		t.Fatalf("expected unsafe MCP config to fail")
	}
	message := err.Error()
	if !strings.Contains(message, "MULTICODEX_OIDC_JWKS_URL is required") {
		t.Fatalf("missing OIDC JWKS error: %s", message)
	}
	if !strings.Contains(message, "MULTICODEX_DATABASE_URL must include a non-empty password") {
		t.Fatalf("missing database password error: %s", message)
	}
}

func TestValidateProductionRequiresAgentDTokenWhenExposed(t *testing.T) {
	cfg := Config{
		Environment:  "production",
		AgentDListen: ":7070",
	}
	err := ValidateProduction(cfg, "worker-agentd")
	if err == nil || !strings.Contains(err.Error(), "MULTICODEX_AGENTD_TOKEN is required") {
		t.Fatalf("expected exposed agentd without token to fail, got %v", err)
	}

	cfg.AgentDListen = "127.0.0.1:7070"
	if err := ValidateProduction(cfg, "worker-agentd"); err != nil {
		t.Fatalf("loopback-only agentd should not require token: %v", err)
	}
}

func TestValidateProductionRejectsDockerExecutorWithoutIsolatedBoundary(t *testing.T) {
	cfg := strictProductionConfig()
	cfg.ExecutorMode = "docker"
	cfg.WorkerDockerSocketEnabled = false
	cfg.WorkerDockerSocketBoundary = ""
	err := ValidateProduction(cfg, "api")
	if err == nil {
		t.Fatalf("expected docker executor without boundary to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"MULTICODEX_WORKER_DOCKER_SOCKET_ENABLED must be true",
		"MULTICODEX_WORKER_DOCKER_SOCKET_BOUNDARY must be isolated-worker-host",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}

	cfg.WorkerDockerSocketEnabled = true
	cfg.WorkerDockerSocketBoundary = "isolated-worker-host"
	if err := ValidateProduction(cfg, "api"); err != nil {
		t.Fatalf("isolated docker boundary should pass: %v", err)
	}
}

func TestValidateProductionRejectsLiveGitSyncWithoutPilotReview(t *testing.T) {
	cfg := strictProductionConfig()
	cfg.GitSyncMode = "live"
	cfg.GitCredentialProvider = "env"
	cfg.GitSyncLiveReviewed = false
	cfg.GitHubToken = ""
	cfg.GitLabToken = ""
	err := ValidateProduction(cfg, "api")
	if err == nil {
		t.Fatalf("expected live Git Sync without review and credentials to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"MULTICODEX_GIT_SYNC_LIVE_REVIEWED must be true",
		"GITHUB_TOKEN or GITLAB_TOKEN is required",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}

	cfg.GitSyncLiveReviewed = true
	cfg.GitHubToken = "gh-live-token"
	if err := ValidateProduction(cfg, "api"); err != nil {
		t.Fatalf("live Git Sync with review and credential should pass: %v", err)
	}
}

func TestValidateProductionRequiresLiveGitCredentialProviderConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{
			name: "file path",
			mutate: func(cfg *Config) {
				cfg.GitCredentialProvider = "file"
				cfg.GitCredentialFilePath = ""
			},
			want: "MULTICODEX_GIT_CREDENTIAL_FILE_PATH is required",
		},
		{
			name: "vault connection",
			mutate: func(cfg *Config) {
				cfg.GitCredentialProvider = "vault"
				cfg.GitVaultAddress = ""
				cfg.GitVaultToken = ""
				cfg.GitVaultTokenFile = ""
				cfg.GitVaultSecretPath = ""
			},
			want: "MULTICODEX_GIT_VAULT_ADDR is required",
		},
		{
			name: "unknown provider",
			mutate: func(cfg *Config) {
				cfg.GitCredentialProvider = "plain-text"
			},
			want: "MULTICODEX_GIT_CREDENTIAL_PROVIDER must be env, file, or vault",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := strictProductionConfig()
			cfg.GitSyncMode = "live"
			cfg.GitSyncLiveReviewed = true
			tt.mutate(&cfg)
			err := ValidateProduction(cfg, "mcp-gateway")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
		})
	}
}

func strictProductionConfig() Config {
	return Config{
		Environment:              "production",
		AuthMode:                 "oidc",
		AuthCookieSecure:         true,
		OIDCIssuer:               "https://issuer.example",
		OIDCAudience:             "multi-codex",
		OIDCJWKSURL:              "https://issuer.example/.well-known/jwks.json",
		OIDCClientID:             "multi-codex-web",
		OIDCRedirectURL:          "https://multi-codex.example/api/v1/auth/callback",
		DatabaseURL:              "postgres://multi_codex:secret@postgres:5432/multi_codex?sslmode=require",
		CORSAllowedOrigins:       []string{"https://multi-codex.example"},
		AgentDURL:                "http://worker-agentd:7070",
		AgentDToken:              "agentd-secret",
		RetentionEnabled:         true,
		RetentionInterval:        time.Hour,
		RetentionMaxAge:          720 * time.Hour,
		AuditShipEnabled:         true,
		AuditShipInterval:        24 * time.Hour,
		AuditSealRoot:            "/var/lib/multi-codex/audit-seals",
		AuditShipTarget:          "s3://audit-bucket/multi-codex",
		MCPSessionTTL:            8 * time.Hour,
		MCPLiveFanoutInterval:    time.Second,
		WorkerDefaultTimeout:     time.Hour,
		QueueDispatchInterval:    5 * time.Second,
		TelemetryPushInterval:    time.Minute,
		AuthSessionTTL:           12 * time.Hour,
		AuthLoginStateTTL:        10 * time.Minute,
		SSHConnectTimeout:        15 * time.Second,
		WorkerCPUs:               "1",
		WorkerMemory:             "2g",
		WorkerPidsLimit:          256,
		WorkerReadOnlyRootFS:     true,
		WorkerTmpfsSize:          "256m",
		WorkerNoNewPrivileges:    true,
		WorkerCapDrop:            []string{"ALL"},
		WorkerCommandDenylist:    []string{"docker", "git push"},
		WorkerSecretProvider:     "env",
		GitCredentialProvider:    "env",
		OIDCClientAuthMethod:     "client_secret_post",
		OIDCPostLoginRedirectURL: "/",
		OIDCDefaultRole:          "viewer",
	}
}
