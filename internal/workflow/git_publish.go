package workflow

import "github.com/Chiiz0/multi-codex/internal/domain"

func RenderPRPublishPlan(task domain.Task, repo domain.Repository, prBodyArtifactID string) map[string]any {
	provider := repo.Provider
	if provider == "" {
		provider = "unknown"
	}
	return map[string]any{
		"provider":            provider,
		"remote_url":          repo.RemoteURL,
		"base_branch":         task.Envelope.BaseBranch,
		"source_branch":       task.Envelope.TargetBranch,
		"title":               task.Title,
		"body_artifact_id":    prBodyArtifactID,
		"required_approval":   "pr_publish",
		"auto_merge":          false,
		"push_command":        "git push origin " + task.Envelope.TargetBranch,
		"provider_operation":  "create_pull_request",
		"credential_required": credentialForProvider(provider),
	}
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
