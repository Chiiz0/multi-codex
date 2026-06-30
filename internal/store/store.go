package store

import (
	"context"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

type Store interface {
	GetAuthContext() domain.AuthContext
	UpsertExternalUser(provider string, subject string, email string, displayName string, role string, orgID string) (domain.AuthContext, error)
	RevokeAuthToken(revocation domain.AuthTokenRevocation) (domain.AuthTokenRevocation, error)
	IsAuthTokenRevoked(tokenHash string, now time.Time) bool
	CleanupAuthTokenRevocations(cutoff time.Time, dryRun bool) (domain.AuthTokenRevocationRetentionResult, error)
	CreateAuthSession(tokenHash string, auth domain.AuthContext, provider string, subject string, expiresAt time.Time) (domain.AuthSession, error)
	CreateAuthSessionWithExternalID(tokenHash string, auth domain.AuthContext, provider string, subject string, externalSessionID string, expiresAt time.Time) (domain.AuthSession, error)
	GetAuthSession(tokenHash string, now time.Time) (domain.AuthContext, domain.AuthSession, error)
	RevokeAuthSession(tokenHash string, revokedAt time.Time) error
	RevokeAuthSessionsByExternalSessionID(provider string, externalSessionID string, revokedAt time.Time) (int64, error)
	RevokeAuthSessionsBySubject(provider string, subject string, revokedAt time.Time) (int64, error)
	CleanupAuthSessions(cutoff time.Time, dryRun bool) (domain.AuthSessionRetentionResult, error)
	CreateAuthLoginState(state domain.AuthLoginState) (domain.AuthLoginState, error)
	ConsumeAuthLoginState(stateHash string, now time.Time) (domain.AuthLoginState, error)
	CleanupAuthLoginStates(cutoff time.Time, dryRun bool) (domain.AuthLoginStateRetentionResult, error)
	GetUserByEmail(email string) (domain.AuthContext, string, error)
	SetUserPassword(userID string, passwordHash string) error
	ListUsers(orgID string) []domain.User
	ListMemberships(orgID string) []domain.Membership
	UpsertUser(user domain.User, orgID string, role string) (domain.AuthContext, error)
	ListOrganizations() []domain.Organization
	CreateOrganization(org domain.Organization) (domain.Organization, error)
	ListProjects() []domain.Project
	CreateProject(project domain.Project) domain.Project
	GetProject(id string) (domain.Project, error)
	ListProjectMemberships(projectID string) []domain.ProjectMembership
	ListProjectMembershipsForUser(userID string) []domain.ProjectMembership
	UpsertProjectMembership(membership domain.ProjectMembership) (domain.ProjectMembership, error)
	ListRepositories(projectID string) []domain.Repository
	CreateRepository(repo domain.Repository) domain.Repository
	GetRepository(id string) (domain.Repository, error)
	ListSkills() []domain.Skill
	ListSkillVersions(skillID string) []domain.SkillVersion
	CreateSkill(skill domain.Skill, version domain.SkillVersion) (domain.Skill, error)
	ListAgentProfiles(projectID string) []domain.AgentProfile
	CreateAgentProfile(profile domain.AgentProfile) (domain.AgentProfile, error)
	ListExecutorNodes() []domain.ExecutorNode
	GetExecutorNode(id string) (domain.ExecutorNode, error)
	RegisterExecutorNode(node domain.ExecutorNode) (domain.ExecutorNode, error)
	VerifyExecutorNodeHostKey(id string, observedFingerprint string) (domain.ExecutorNode, error)
	CreateTask(envelope domain.TaskEnvelope) domain.Task
	GetTask(id string) (domain.Task, error)
	ListTasks(projectID string) []domain.Task
	UpdateTaskStatus(taskID string, status string) (domain.Task, error)
	StartRun(taskID string, role string, executor string) (domain.Run, error)
	EnqueueRun(taskID string, role string, executor string, priority int, attempt int, maxAttempts int, reason string) (domain.Run, error)
	DispatchQueuedRun() (domain.Run, error)
	GetRun(id string) (domain.Run, error)
	UpdateRunWorkspace(runID string, branch string, worktreePath string) (domain.Run, error)
	ListAllRuns() []domain.Run
	ListRuns(taskID string) []domain.Run
	FinishRun(runID string, status string, result map[string]any) (domain.Run, error)
	ListEvents(runID string) []domain.RunEvent
	AddEvent(runID string, level string, eventType string, message string, payload map[string]any) (domain.RunEvent, error)
	CreateArtifact(artifact domain.Artifact) (domain.Artifact, error)
	GetArtifact(id string) (domain.Artifact, error)
	ListArtifacts(runID string) []domain.Artifact
	RecordScopeCheck(taskID string, runID string, baseRef string, result domain.ScopeCheckResult) (domain.ScopeCheckRecord, error)
	LatestScopeCheck(taskID string) (domain.ScopeCheckRecord, error)
	ListApprovals(taskID string) []domain.Approval
	CreateApproval(approval domain.Approval) (domain.Approval, error)
	DecideApproval(approvalID string, status string, approvedBy string, reason string) (domain.Approval, error)
	RecordToolCall(call domain.ToolCall) domain.ToolCall
	ListToolCalls() []domain.ToolCall
	UpsertMCPSession(session domain.MCPSession) (domain.MCPSession, error)
	GetMCPSession(id string) (domain.MCPSession, error)
	AppendMCPSessionEvent(sessionID string, eventType string, payload map[string]any) (domain.MCPSessionEvent, error)
	ListMCPSessionEventsAfter(sessionID string, afterSeq int64, limit int) []domain.MCPSessionEvent
	CleanupMCPSessions(cutoff time.Time, dryRun bool) (domain.MCPSessionRetentionResult, error)
	ListAuditLogs() []domain.AuditLog
	ListAuditLogsForSeal() []domain.AuditLog
	RecordAuditLog(entry domain.AuditLog) domain.AuditLog
}

type MCPSessionEventSubscriber interface {
	SubscribeMCPSessionEvents(ctx context.Context) (<-chan domain.MCPSessionEventNotification, func(), error)
}
