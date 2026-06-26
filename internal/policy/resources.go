package policy

import (
	"fmt"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func ValidateTaskWithResources(st store.Store, envelope domain.TaskEnvelope) domain.ValidationResult {
	result := ValidateTaskEnvelope(envelope)

	if _, err := st.GetRepository(envelope.RepositoryID); err != nil {
		result.Errors = append(result.Errors, "repository_id does not reference a known repository")
	}

	var skillFound bool
	for _, skill := range st.ListSkills() {
		if skill.Name != envelope.Skill {
			continue
		}
		skillFound = true
		if !skill.Enabled {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q is disabled", envelope.Skill))
		}
		if normalizeRole(skill.Role) != normalizeRole(envelope.Role) && normalizeRole(skill.Role) != "main" {
			result.Errors = append(result.Errors, fmt.Sprintf("skill %q role %q does not match task role %q", envelope.Skill, skill.Role, envelope.Role))
		}
		break
	}
	if !skillFound {
		result.Errors = append(result.Errors, "skill does not exist")
	}

	var profileFound bool
	for _, profile := range st.ListAgentProfiles(envelope.ProjectID) {
		if profile.Name != envelope.AgentProfile {
			continue
		}
		profileFound = true
		if normalizeRole(profile.Role) != normalizeRole(envelope.Role) {
			result.Errors = append(result.Errors, fmt.Sprintf("agent profile %q role %q does not match task role %q", envelope.AgentProfile, profile.Role, envelope.Role))
		}
		if profile.Executor != envelope.Executor {
			result.Errors = append(result.Errors, fmt.Sprintf("agent profile %q executor %q does not match task executor %q", envelope.AgentProfile, profile.Executor, envelope.Executor))
		}
		if envelope.Network && !profile.NetworkEnabled {
			result.Errors = append(result.Errors, "task requests network but agent profile disables network")
		}
		break
	}
	if !profileFound {
		result.Errors = append(result.Errors, "agent_profile does not exist")
	}

	var executorAvailable bool
	for _, node := range st.ListExecutorNodes() {
		if node.Kind != envelope.Executor {
			continue
		}
		if envelope.Executor == "ssh" {
			if node.Status == "active" && node.HostKeyVerified && node.AgentDURL != "" {
				executorAvailable = true
				break
			}
			continue
		}
		if node.Status == "active" || node.Status == "registered" {
			executorAvailable = true
			break
		}
	}
	if !executorAvailable {
		result.Errors = append(result.Errors, "executor has no available node")
	}

	result.Valid = len(result.Errors) == 0
	return result
}

func normalizeRole(role string) string {
	if role == "git-sync" {
		return "git_sync"
	}
	return role
}
