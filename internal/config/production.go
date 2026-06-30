package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func (c Config) IsProduction() bool {
	env := strings.ToLower(strings.TrimSpace(c.Environment))
	return c.ProductionMode || env == "production" || env == "prod"
}

func ValidateProduction(c Config, service string) error {
	if !c.IsProduction() {
		return nil
	}
	service = strings.TrimSpace(service)
	var issues []string

	switch service {
	case "api":
		issues = append(issues, validateProductionAuth(c, true)...)
		issues = append(issues, validateProductionDatabase(c)...)
		issues = append(issues, validateProductionCORS(c)...)
		issues = append(issues, validateProductionAgentDClient(c)...)
		issues = append(issues, validateProductionWorkerControls(c)...)
		issues = append(issues, validateProductionGitSync(c)...)
		issues = append(issues, validateProductionAuditAndRetention(c)...)
	case "mcp-gateway":
		issues = append(issues, validateProductionAuth(c, false)...)
		issues = append(issues, validateProductionDatabase(c)...)
		issues = append(issues, validateProductionAgentDClient(c)...)
		issues = append(issues, validateProductionWorkerControls(c)...)
		issues = append(issues, validateProductionGitSync(c)...)
	case "worker-agentd":
		issues = append(issues, validateProductionAgentDServer(c)...)
	default:
		issues = append(issues, "unknown production service "+service)
	}

	if len(issues) > 0 {
		return fmt.Errorf("unsafe production configuration for %s: %s", service, strings.Join(issues, "; "))
	}
	return nil
}

func validateProductionAuth(c Config, browserSession bool) []string {
	var issues []string
	if !strings.EqualFold(strings.TrimSpace(c.AuthMode), "oidc") {
		issues = append(issues, "MULTICODEX_AUTH_MODE must be oidc")
	}
	if strings.TrimSpace(c.OIDCIssuer) == "" {
		issues = append(issues, "MULTICODEX_OIDC_ISSUER is required")
	}
	if strings.TrimSpace(c.OIDCAudience) == "" {
		issues = append(issues, "MULTICODEX_OIDC_AUDIENCE is required")
	}
	if strings.TrimSpace(c.OIDCJWKSURL) == "" {
		issues = append(issues, "MULTICODEX_OIDC_JWKS_URL is required")
	}
	if browserSession {
		if !c.AuthCookieSecure {
			issues = append(issues, "MULTICODEX_AUTH_COOKIE_SECURE must be true")
		}
		if strings.TrimSpace(c.OIDCClientID) == "" {
			issues = append(issues, "MULTICODEX_OIDC_CLIENT_ID is required")
		}
		if strings.TrimSpace(c.OIDCRedirectURL) == "" {
			issues = append(issues, "MULTICODEX_OIDC_REDIRECT_URL is required")
		}
	}
	return issues
}

func validateProductionDatabase(c Config) []string {
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return []string{"MULTICODEX_DATABASE_URL is required"}
	}
	if !databaseURLHasPassword(c.DatabaseURL) {
		return []string{"MULTICODEX_DATABASE_URL must include a non-empty password"}
	}
	return nil
}

func validateProductionCORS(c Config) []string {
	if len(c.CORSAllowedOrigins) == 0 {
		return []string{"MULTICODEX_CORS_ALLOWED_ORIGINS must list explicit HTTPS origins"}
	}
	var issues []string
	for _, origin := range c.CORSAllowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		if origin == "*" {
			issues = append(issues, "MULTICODEX_CORS_ALLOWED_ORIGINS cannot contain wildcard origins")
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" {
			issues = append(issues, "MULTICODEX_CORS_ALLOWED_ORIGINS entries must be HTTPS origins without paths")
		}
	}
	return issues
}

func validateProductionAgentDClient(c Config) []string {
	if strings.TrimSpace(c.AgentDURL) == "" {
		return nil
	}
	if strings.TrimSpace(c.AgentDToken) == "" {
		return []string{"MULTICODEX_AGENTD_TOKEN is required when worker-agentd is configured"}
	}
	return nil
}

func validateProductionAgentDServer(c Config) []string {
	if !agentDListenExposed(c.AgentDListen) {
		return nil
	}
	if strings.TrimSpace(c.AgentDToken) == "" {
		return []string{"MULTICODEX_AGENTD_TOKEN is required when worker-agentd listens on a non-loopback address"}
	}
	return nil
}

func validateProductionWorkerControls(c Config) []string {
	var issues []string
	if c.WorkerDefaultTimeout <= 0 {
		issues = append(issues, "MULTICODEX_WORKER_DEFAULT_TIMEOUT must be positive")
	}
	if strings.TrimSpace(c.WorkerCPUs) == "" {
		issues = append(issues, "MULTICODEX_WORKER_CPUS is required")
	}
	if strings.TrimSpace(c.WorkerMemory) == "" {
		issues = append(issues, "MULTICODEX_WORKER_MEMORY is required")
	}
	if c.WorkerPidsLimit <= 0 {
		issues = append(issues, "MULTICODEX_WORKER_PIDS_LIMIT must be positive")
	}
	if !c.WorkerReadOnlyRootFS {
		issues = append(issues, "MULTICODEX_WORKER_READ_ONLY_ROOTFS must be true")
	}
	if strings.TrimSpace(c.WorkerTmpfsSize) == "" {
		issues = append(issues, "MULTICODEX_WORKER_TMPFS_SIZE is required")
	}
	if !c.WorkerNoNewPrivileges {
		issues = append(issues, "MULTICODEX_WORKER_NO_NEW_PRIVILEGES must be true")
	}
	if !containsFold(c.WorkerCapDrop, "ALL") {
		issues = append(issues, "MULTICODEX_WORKER_CAP_DROP must include ALL")
	}
	if len(c.WorkerCommandDenylist) == 0 {
		issues = append(issues, "MULTICODEX_WORKER_COMMAND_DENYLIST must not be empty")
	}
	if strings.EqualFold(strings.TrimSpace(c.ExecutorMode), "docker") {
		if !c.WorkerDockerSocketEnabled {
			issues = append(issues, "MULTICODEX_WORKER_DOCKER_SOCKET_ENABLED must be true when MULTICODEX_EXECUTOR_MODE=docker in production")
		}
		if strings.TrimSpace(c.WorkerDockerSocketBoundary) != "isolated-worker-host" {
			issues = append(issues, "MULTICODEX_WORKER_DOCKER_SOCKET_BOUNDARY must be isolated-worker-host when docker executor is enabled in production")
		}
	}
	return issues
}

func validateProductionAuditAndRetention(c Config) []string {
	var issues []string
	if !c.RetentionEnabled {
		issues = append(issues, "MULTICODEX_RETENTION_ENABLED must be true")
	}
	if c.RetentionInterval <= 0 {
		issues = append(issues, "MULTICODEX_RETENTION_INTERVAL must be positive")
	}
	if c.RetentionMaxAge <= 0 {
		issues = append(issues, "MULTICODEX_RETENTION_MAX_AGE must be positive")
	}
	if !c.AuditShipEnabled {
		issues = append(issues, "MULTICODEX_AUDIT_SHIP_ENABLED must be true")
	}
	if c.AuditShipInterval <= 0 {
		issues = append(issues, "MULTICODEX_AUDIT_SHIP_INTERVAL must be positive")
	}
	if strings.TrimSpace(c.AuditSealRoot) == "" {
		issues = append(issues, "MULTICODEX_AUDIT_SEAL_ROOT is required")
	}
	if strings.TrimSpace(c.AuditShipTarget) == "" {
		issues = append(issues, "MULTICODEX_AUDIT_SHIP_TARGET is required")
	}
	return issues
}

func validateProductionGitSync(c Config) []string {
	mode := strings.ToLower(strings.TrimSpace(c.GitSyncMode))
	if mode == "" {
		mode = "dry-run"
	}
	if mode != "dry-run" && mode != "live" {
		return []string{"MULTICODEX_GIT_SYNC_MODE must be dry-run or live"}
	}
	if mode != "live" {
		return nil
	}
	var issues []string
	if !c.GitSyncLiveReviewed {
		issues = append(issues, "MULTICODEX_GIT_SYNC_LIVE_REVIEWED must be true after dry-run audit evidence is reviewed")
	}
	provider := strings.ToLower(strings.TrimSpace(c.GitCredentialProvider))
	if provider == "" {
		provider = "env"
	}
	switch provider {
	case "env":
		if strings.TrimSpace(c.GitHubToken) == "" && strings.TrimSpace(c.GitLabToken) == "" {
			issues = append(issues, "GITHUB_TOKEN or GITLAB_TOKEN is required when live Git Sync uses env credentials")
		}
	case "file":
		if strings.TrimSpace(c.GitCredentialFilePath) == "" {
			issues = append(issues, "MULTICODEX_GIT_CREDENTIAL_FILE_PATH is required when live Git Sync uses file credentials")
		}
	case "vault":
		if strings.TrimSpace(c.GitVaultAddress) == "" {
			issues = append(issues, "MULTICODEX_GIT_VAULT_ADDR is required when live Git Sync uses Vault credentials")
		}
		if strings.TrimSpace(c.GitVaultToken) == "" && strings.TrimSpace(c.GitVaultTokenFile) == "" {
			issues = append(issues, "MULTICODEX_GIT_VAULT_TOKEN or MULTICODEX_GIT_VAULT_TOKEN_FILE is required when live Git Sync uses Vault credentials")
		}
		if strings.TrimSpace(c.GitVaultSecretPath) == "" {
			issues = append(issues, "MULTICODEX_GIT_VAULT_SECRET_PATH is required when live Git Sync uses Vault credentials")
		}
	default:
		issues = append(issues, "MULTICODEX_GIT_CREDENTIAL_PROVIDER must be env, file, or vault")
	}
	return issues
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func databaseURLHasPassword(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "postgres" && scheme != "postgresql" {
		return false
	}
	if parsed.User == nil {
		return false
	}
	password, ok := parsed.User.Password()
	return ok && password != ""
}

func agentDListenExposed(listen string) bool {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return false
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		if strings.HasPrefix(listen, ":") {
			return true
		}
		return !isLoopbackHost(listen)
	}
	return !isLoopbackHost(host)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}
