package gitsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestPublishPRDryRunPreparesProviderRequest(t *testing.T) {
	result := PublishPR(context.Background(), PublishRequest{
		Task: domain.Task{
			Title: "Add audit export",
			Envelope: domain.TaskEnvelope{
				BaseBranch:   "main",
				TargetBranch: "codex/add-audit-export",
			},
		},
		Repository:     domain.Repository{Provider: "github", RemoteURL: "https://github.com/example/repo.git"},
		Body:           "PR body",
		BodyArtifactID: "artifact-1",
		Config:         config.Config{GitSyncMode: "dry-run"},
	})

	if result.Status != "publish_prepared" {
		t.Fatalf("status = %q", result.Status)
	}
	if !result.DryRun {
		t.Fatalf("expected dry run")
	}
	if result.AutoMerge {
		t.Fatalf("auto merge must stay disabled")
	}
	if result.CredentialName != "GITHUB_TOKEN" {
		t.Fatalf("credential = %q", result.CredentialName)
	}
}

func TestPublishPRLiveBlocksWithoutCredential(t *testing.T) {
	result := PublishPR(context.Background(), PublishRequest{
		Task: domain.Task{
			Title: "Add audit export",
			Envelope: domain.TaskEnvelope{
				BaseBranch:   "main",
				TargetBranch: "codex/add-audit-export",
			},
		},
		Repository: domain.Repository{Provider: "github", RemoteURL: "https://github.com/example/repo.git"},
		Body:       "PR body",
		Config:     config.Config{GitSyncMode: "live", GitHubAPIURL: "https://api.github.com"},
	})

	if result.Status != "blocked" {
		t.Fatalf("status = %q", result.Status)
	}
	if len(result.Errors) == 0 {
		t.Fatalf("expected missing credential error")
	}
}

func TestPublishPRLiveUsesFileCredentialResolverForGitHub(t *testing.T) {
	secretFile := filepath.Join(t.TempDir(), "git-secrets.json")
	if err := os.WriteFile(secretFile, []byte(`{"GITHUB_TOKEN":"gh-file-token"}`), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	var gotAuthorization string
	var gotPayload map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo/pulls" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		gotAuthorization = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode provider payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/example/repo/pull/7","number":7}`))
	}))
	defer provider.Close()

	result := PublishPR(context.Background(), PublishRequest{
		Task:           publishTask(),
		Repository:     domain.Repository{Provider: "github", RemoteURL: "https://github.com/example/repo.git"},
		Body:           "PR body",
		BodyArtifactID: "artifact-1",
		Config: config.Config{
			GitSyncMode:           "live",
			GitHubAPIURL:          provider.URL,
			GitCredentialProvider: "file",
			GitCredentialFilePath: secretFile,
		},
	})
	if result.Status != "published" || result.PRURL == "" || result.AutoMerge {
		t.Fatalf("publish result = %#v", result)
	}
	if result.CredentialProvider != "file" || !result.CredentialResolved {
		t.Fatalf("credential metadata = %#v", result)
	}
	if gotAuthorization != "token gh-file-token" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
	if gotPayload["head"] != "codex/add-audit-export" || gotPayload["base"] != "main" {
		t.Fatalf("payload = %#v", gotPayload)
	}
}

func TestPublishPRLiveUsesVaultCredentialResolverForGitLab(t *testing.T) {
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/kv/data/multi-codex/git" {
			t.Fatalf("vault path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Vault-Token") != "vault-token" {
			t.Fatalf("vault token = %q", r.Header.Get("X-Vault-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"data":{"GITLAB_TOKEN":"gl-vault-token"}}}`))
	}))
	defer vault.Close()
	var gotAuthorization string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/projects/group%2Frepo/merge_requests" {
			t.Fatalf("path = %q", r.URL.EscapedPath())
		}
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web_url":"https://gitlab.com/group/repo/-/merge_requests/11","iid":11}`))
	}))
	defer provider.Close()

	result := PublishPR(context.Background(), PublishRequest{
		Task:           publishTask(),
		Repository:     domain.Repository{Provider: "gitlab", RemoteURL: "https://gitlab.com/group/repo.git"},
		Body:           "PR body",
		BodyArtifactID: "artifact-1",
		Config: config.Config{
			GitSyncMode:           "live",
			GitLabAPIURL:          provider.URL,
			GitCredentialProvider: "vault",
			GitVaultAddress:       vault.URL,
			GitVaultToken:         "vault-token",
			GitVaultMount:         "kv",
			GitVaultSecretPath:    "multi-codex/git",
		},
	})
	if result.Status != "published" || result.PRURL == "" || result.AutoMerge {
		t.Fatalf("publish result = %#v", result)
	}
	if result.CredentialProvider != "vault" || !result.CredentialResolved {
		t.Fatalf("credential metadata = %#v", result)
	}
	if gotAuthorization != "Bearer gl-vault-token" {
		t.Fatalf("authorization = %q", gotAuthorization)
	}
}

func TestPublishPRProviderErrorRedactsCredential(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token gh-env-token was rejected", http.StatusUnauthorized)
	}))
	defer provider.Close()
	result := PublishPR(context.Background(), PublishRequest{
		Task:           publishTask(),
		Repository:     domain.Repository{Provider: "github", RemoteURL: "https://github.com/example/repo.git"},
		Body:           "PR body",
		BodyArtifactID: "artifact-1",
		Config: config.Config{
			GitSyncMode:           "live",
			GitHubAPIURL:          provider.URL,
			GitHubToken:           "gh-env-token",
			GitCredentialProvider: "file",
		},
	})
	if result.Status != "blocked" || len(result.Errors) == 0 {
		t.Fatalf("publish result = %#v", result)
	}
	if result.CredentialProvider != "env" || !result.CredentialResolved {
		t.Fatalf("credential metadata = %#v", result)
	}
	if result.Errors[0] == "" || strings.Contains(result.Errors[0], "gh-env-token") {
		t.Fatalf("error leaked token: %#v", result.Errors)
	}
}

func TestParseHostedRepoSupportsSSH(t *testing.T) {
	owner, repo, err := parseHostedRepo("git@github.com:example/repo.git", "github.com")
	if err != nil {
		t.Fatalf("parse SSH remote: %v", err)
	}
	if owner != "example" || repo != "repo" {
		t.Fatalf("owner/repo = %s/%s", owner, repo)
	}
}

func publishTask() domain.Task {
	return domain.Task{
		Title: "Add audit export",
		Envelope: domain.TaskEnvelope{
			BaseBranch:   "main",
			TargetBranch: "codex/add-audit-export",
		},
	}
}
