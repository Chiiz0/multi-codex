package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

const (
	defaultOrgID        = "00000000-0000-7000-8000-000000000001"
	defaultUserID       = "00000000-0000-7000-8000-000000000011"
	defaultProjectID    = "00000000-0000-7000-8000-000000000101"
	defaultRepoID       = "00000000-0000-7000-8000-000000000201"
	defaultDockerNodeID = "00000000-0000-7000-8000-000000000301"
	defaultSSHNodeID    = "00000000-0000-7000-8000-000000000302"
)

type seedSkillVersion struct {
	Skill   domain.Skill
	Version domain.SkillVersion
}

func seedSkills(now time.Time) []domain.Skill {
	seeds := seedSkillVersions(now)
	skills := make([]domain.Skill, 0, len(seeds))
	for _, seed := range seeds {
		skills = append(skills, seed.Skill)
	}
	return skills
}

func seedSkillVersions(now time.Time) []seedSkillVersion {
	definitions := []struct {
		name        string
		role        string
		description string
		path        string
	}{
		{"company-main-gateway", "main", "Main Codex gateway policy and task orchestration instructions.", "skills/company-main-gateway/SKILL.md"},
		{"company-feature-worker", "feature", "Feature implementation worker instructions.", "skills/company-feature-worker/SKILL.md"},
		{"company-test-worker", "test", "Independent test worker instructions.", "skills/company-test-worker/SKILL.md"},
		{"company-audit-worker", "audit", "Read-only security and architecture audit worker instructions.", "skills/company-audit-worker/SKILL.md"},
		{"company-git-sync", "git_sync", "Git synchronization and PR body preparation worker instructions.", "skills/company-git-sync/SKILL.md"},
	}

	seeds := make([]seedSkillVersion, 0, len(definitions))
	for _, definition := range definitions {
		hash := deterministicHash(definition.name + ":" + definition.path)
		skill := domain.Skill{
			ID:            "skill_" + definition.name,
			Name:          definition.name,
			Role:          definition.role,
			Description:   definition.description,
			Enabled:       true,
			LatestVersion: "local",
			ContentHash:   hash,
			Path:          definition.path,
			CreatedAt:     now,
		}
		version := domain.SkillVersion{
			ID:          "skillver_" + definition.name,
			SkillID:     skill.ID,
			Version:     "local",
			ContentHash: hash,
			Path:        definition.path,
			CreatedAt:   now,
		}
		seeds = append(seeds, seedSkillVersion{Skill: skill, Version: version})
	}
	return seeds
}

func seedProfiles(projectID string, now time.Time) []domain.AgentProfile {
	return []domain.AgentProfile{
		{
			ID:             "profile_feature_go_node",
			ProjectID:      projectID,
			Name:           "feature-worker-go-node",
			Role:           "feature",
			Model:          "gpt-5",
			SandboxMode:    "workspace-write",
			ApprovalPolicy: "never",
			Executor:       "docker",
			Image:          "multi-codex/codex-worker:go1.25-node-vite8",
			NetworkEnabled: false,
			Config:         map[string]any{"timeout_seconds": 3600, "collect_diff": true},
			CreatedAt:      now,
		},
		{
			ID:             "profile_test_go_node",
			ProjectID:      projectID,
			Name:           "test-worker-go-node",
			Role:           "test",
			Model:          "gpt-5",
			SandboxMode:    "workspace-write",
			ApprovalPolicy: "never",
			Executor:       "docker",
			Image:          "multi-codex/codex-worker:go1.25-node-vite8",
			NetworkEnabled: false,
			Config:         map[string]any{"timeout_seconds": 3600, "required_commands_only": true},
			CreatedAt:      now,
		},
		{
			ID:             "profile_audit_readonly",
			ProjectID:      projectID,
			Name:           "audit-worker-readonly",
			Role:           "audit",
			Model:          "gpt-5",
			SandboxMode:    "read-only",
			ApprovalPolicy: "never",
			Executor:       "docker",
			Image:          "multi-codex/codex-worker:go1.25-node-vite8",
			NetworkEnabled: false,
			Config:         map[string]any{"timeout_seconds": 2400, "read_only": true},
			CreatedAt:      now,
		},
		{
			ID:             "profile_git_sync",
			ProjectID:      projectID,
			Name:           "git-sync-worker",
			Role:           "git_sync",
			Model:          "gpt-5",
			SandboxMode:    "workspace-write",
			ApprovalPolicy: "on-request",
			Executor:       "docker",
			Image:          "multi-codex/codex-worker:go1.25-node-vite8",
			NetworkEnabled: false,
			Config:         map[string]any{"prepare_pr_only": true, "allow_push": false},
			CreatedAt:      now,
		},
	}
}

func seedNodes(now time.Time) []domain.ExecutorNode {
	return []domain.ExecutorNode{
		{
			ID:              defaultDockerNodeID,
			Kind:            "docker",
			Name:            "local-docker",
			Address:         "unix:///var/run/docker.sock",
			HostKeyVerified: true,
			Labels:          map[string]any{"local": true, "executor": "docker"},
			Capacity:        map[string]any{"concurrency": 1},
			Status:          "active",
			CreatedAt:       now,
		},
		{
			ID:                 defaultSSHNodeID,
			Kind:               "ssh",
			Name:               "ssh-agentd-poc",
			Address:            "codex-worker@example.invalid:22",
			AgentDURL:          "http://worker-agentd-dev:7070",
			HostKeyFingerprint: "SHA256:multi-codex-agentd-dev",
			ForcedCommand:      "multi-codex-worker-agentd --forced-command",
			Labels:             map[string]any{"poc": true, "executor": "ssh"},
			Capacity:           map[string]any{"concurrency": 1},
			Status:             "registered",
			CreatedAt:          now,
		},
	}
}

func deterministicHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
