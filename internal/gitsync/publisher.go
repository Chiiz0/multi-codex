package gitsync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/secrets"
)

type PublishRequest struct {
	Task           domain.Task
	Repository     domain.Repository
	Body           string
	BodyArtifactID string
	Config         config.Config
	Credentials    secrets.Resolver
}

type PublishResult struct {
	Status             string         `json:"status"`
	Summary            string         `json:"summary"`
	Provider           string         `json:"provider"`
	RemoteURL          string         `json:"remote_url"`
	BaseBranch         string         `json:"base_branch"`
	SourceBranch       string         `json:"source_branch"`
	Title              string         `json:"title"`
	BodyArtifactID     string         `json:"body_artifact_id"`
	RequiredApproval   string         `json:"required_approval"`
	AutoMerge          bool           `json:"auto_merge"`
	DryRun             bool           `json:"dry_run"`
	ProviderOperation  string         `json:"provider_operation"`
	CredentialName     string         `json:"credential_required"`
	CredentialProvider string         `json:"credential_provider"`
	CredentialResolved bool           `json:"credential_resolved"`
	Request            map[string]any `json:"request"`
	PRURL              string         `json:"pr_url,omitempty"`
	PRNumber           any            `json:"pr_number,omitempty"`
	Errors             []string       `json:"errors,omitempty"`
}

func PublishPR(ctx context.Context, req PublishRequest) PublishResult {
	provider := strings.ToLower(req.Repository.Provider)
	if provider == "" {
		provider = inferProvider(req.Repository.RemoteURL)
	}
	result := PublishResult{
		Status:             "publish_prepared",
		Summary:            "PR publish operation prepared. No merge is performed automatically.",
		Provider:           provider,
		RemoteURL:          req.Repository.RemoteURL,
		BaseBranch:         req.Task.Envelope.BaseBranch,
		SourceBranch:       req.Task.Envelope.TargetBranch,
		Title:              req.Task.Title,
		BodyArtifactID:     req.BodyArtifactID,
		RequiredApproval:   "pr_publish",
		AutoMerge:          false,
		DryRun:             req.Config.GitSyncMode != "live",
		ProviderOperation:  "create_pull_request",
		CredentialName:     credentialForProvider(provider),
		CredentialProvider: credentialProviderName(req),
		Request: map[string]any{
			"title": req.Task.Title,
			"head":  req.Task.Envelope.TargetBranch,
			"base":  req.Task.Envelope.BaseBranch,
			"body":  req.Body,
		},
	}
	if result.DryRun {
		result.Summary = "Dry-run PR publish request prepared; set MULTICODEX_GIT_SYNC_MODE=live and provide provider credentials to create it."
		return result
	}

	switch provider {
	case "github":
		return publishGitHub(ctx, req, result)
	case "gitlab":
		return publishGitLab(ctx, req, result)
	default:
		result.Status = "blocked"
		result.Errors = append(result.Errors, "unsupported provider "+provider)
		return result
	}
}

func publishGitHub(ctx context.Context, req PublishRequest, result PublishResult) PublishResult {
	credential, err := providerCredential(req, "github")
	result.CredentialProvider = credential.Provider
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	if !credential.OK {
		result.Status = "blocked"
		result.Errors = append(result.Errors, "GITHUB_TOKEN is required for live GitHub PR creation")
		return result
	}
	result.CredentialResolved = true
	owner, repo, err := parseHostedRepo(req.Repository.RemoteURL, "github.com")
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	endpoint := strings.TrimRight(req.Config.GitHubAPIURL, "/") + "/repos/" + owner + "/" + repo + "/pulls"
	payload := map[string]any{
		"title": req.Task.Title,
		"head":  req.Task.Envelope.TargetBranch,
		"base":  req.Task.Envelope.BaseBranch,
		"body":  req.Body,
	}
	response, err := postJSON(ctx, endpoint, "token "+credential.Value, credential.Value, payload)
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.Status = "published"
	result.Summary = "GitHub pull request was created without auto-merge."
	result.PRURL, _ = response["html_url"].(string)
	result.PRNumber = response["number"]
	return result
}

func publishGitLab(ctx context.Context, req PublishRequest, result PublishResult) PublishResult {
	credential, err := providerCredential(req, "gitlab")
	result.CredentialProvider = credential.Provider
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	if !credential.OK {
		result.Status = "blocked"
		result.Errors = append(result.Errors, "GITLAB_TOKEN is required for live GitLab merge request creation")
		return result
	}
	result.CredentialResolved = true
	projectPath, err := parseGitLabProjectPath(req.Repository.RemoteURL)
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	endpoint := strings.TrimRight(req.Config.GitLabAPIURL, "/") + "/projects/" + url.PathEscape(projectPath) + "/merge_requests"
	payload := map[string]any{
		"title":         req.Task.Title,
		"source_branch": req.Task.Envelope.TargetBranch,
		"target_branch": req.Task.Envelope.BaseBranch,
		"description":   req.Body,
	}
	response, err := postJSON(ctx, endpoint, "Bearer "+credential.Value, credential.Value, payload)
	if err != nil {
		result.Status = "blocked"
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.Status = "published"
	result.Summary = "GitLab merge request was created without auto-merge."
	result.PRURL, _ = response["web_url"].(string)
	result.PRNumber = response["iid"]
	return result
}

func postJSON(ctx context.Context, endpoint string, authorization string, secret string, payload map[string]any) (map[string]any, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", authorization)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("provider returned %d: %s", resp.StatusCode, redactSecret(string(responseBody), secret))
	}
	out := map[string]any{}
	_ = json.Unmarshal(responseBody, &out)
	return out, nil
}

type credentialLookup struct {
	Value    string
	OK       bool
	Provider string
}

func providerCredential(req PublishRequest, provider string) (credentialLookup, error) {
	name := credentialForProvider(provider)
	if name == "GITHUB_TOKEN" && req.Config.GitHubToken != "" {
		return credentialLookup{Value: req.Config.GitHubToken, OK: true, Provider: "env"}, nil
	}
	if name == "GITLAB_TOKEN" && req.Config.GitLabToken != "" {
		return credentialLookup{Value: req.Config.GitLabToken, OK: true, Provider: "env"}, nil
	}
	resolver := req.Credentials
	providerName := credentialProviderName(req)
	if resolver == nil {
		var err error
		resolver, err = gitCredentialResolver(req.Config)
		if err != nil {
			return credentialLookup{Provider: providerName}, err
		}
		providerName = resolver.Provider()
	}
	value, ok, err := resolver.Lookup(name)
	return credentialLookup{Value: value, OK: ok, Provider: providerName}, err
}

func gitCredentialResolver(cfg config.Config) (secrets.Resolver, error) {
	return secrets.NewResolverWithConfig(secrets.ResolverConfig{
		Provider:        cfg.GitCredentialProvider,
		FilePath:        cfg.GitCredentialFilePath,
		VaultAddress:    cfg.GitVaultAddress,
		VaultToken:      cfg.GitVaultToken,
		VaultTokenFile:  cfg.GitVaultTokenFile,
		VaultNamespace:  cfg.GitVaultNamespace,
		VaultMount:      cfg.GitVaultMount,
		VaultSecretPath: cfg.GitVaultSecretPath,
	})
}

func credentialProviderName(req PublishRequest) string {
	if req.Credentials != nil {
		return req.Credentials.Provider()
	}
	if strings.TrimSpace(req.Config.GitCredentialProvider) == "" {
		return "env"
	}
	return req.Config.GitCredentialProvider
}

func redactSecret(value string, secret string) string {
	if len(secret) < 4 {
		return value
	}
	return strings.ReplaceAll(value, secret, "[redacted-secret]")
}

func inferProvider(remoteURL string) string {
	if strings.Contains(remoteURL, "github.com") {
		return "github"
	}
	if strings.Contains(remoteURL, "gitlab.com") {
		return "gitlab"
	}
	return "unknown"
}

func credentialForProvider(provider string) string {
	switch provider {
	case "github":
		return "GITHUB_TOKEN"
	case "gitlab":
		return "GITLAB_TOKEN"
	default:
		return "provider token"
	}
}

func parseHostedRepo(remoteURL string, host string) (string, string, error) {
	path, err := hostedPath(remoteURL, host)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(strings.TrimSuffix(path, ".git"), "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("remote URL does not contain owner/repo: %s", remoteURL)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

func parseGitLabProjectPath(remoteURL string) (string, error) {
	path, err := hostedPath(remoteURL, "gitlab.com")
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, ".git"), nil
}

func hostedPath(remoteURL string, host string) (string, error) {
	if strings.HasPrefix(remoteURL, "git@"+host+":") {
		return strings.TrimPrefix(remoteURL, "git@"+host+":"), nil
	}
	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return "", err
	}
	if parsed.Host != host && !strings.HasSuffix(parsed.Host, "."+host) {
		return "", fmt.Errorf("remote URL host %q does not match %s", parsed.Host, host)
	}
	return strings.TrimPrefix(parsed.Path, "/"), nil
}
