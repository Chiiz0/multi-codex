package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type Project struct {
	ID          string    `json:"id"`
	OrgID       string    `json:"org_id,omitempty"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	Provider        string    `json:"provider"`
	RemoteURL       string    `json:"remote_url"`
	DefaultBranch   string    `json:"default_branch"`
	LocalMirrorPath string    `json:"local_mirror_path,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type User struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	DisplayName      string    `json:"display_name"`
	ExternalProvider string    `json:"external_provider,omitempty"`
	ExternalSubject  string    `json:"external_subject,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Membership struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type ProjectMembership struct {
	ProjectID   string    `json:"project_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	ProjectName string    `json:"project_name,omitempty"`
	ProjectSlug string    `json:"project_slug,omitempty"`
	UserEmail   string    `json:"user_email,omitempty"`
	UserName    string    `json:"user_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type AuthContext struct {
	User               User                `json:"user"`
	Membership         Membership          `json:"membership"`
	ProjectMemberships []ProjectMembership `json:"project_memberships"`
	Permissions        []string            `json:"permissions"`
}

type Skill struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id,omitempty"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Role          string    `json:"role"`
	Enabled       bool      `json:"enabled"`
	LatestVersion string    `json:"version,omitempty"`
	ContentHash   string    `json:"content_hash,omitempty"`
	Path          string    `json:"path,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type SkillVersion struct {
	ID          string    `json:"id"`
	SkillID     string    `json:"skill_id"`
	Version     string    `json:"version"`
	ContentHash string    `json:"content_hash"`
	Path        string    `json:"path"`
	CreatedAt   time.Time `json:"created_at"`
}

type AgentProfile struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"project_id,omitempty"`
	Name           string         `json:"name"`
	Role           string         `json:"role"`
	Model          string         `json:"model"`
	SandboxMode    string         `json:"sandbox_mode"`
	ApprovalPolicy string         `json:"approval_policy"`
	Executor       string         `json:"executor"`
	Image          string         `json:"image,omitempty"`
	NetworkEnabled bool           `json:"network_enabled"`
	Config         map[string]any `json:"config"`
	CreatedAt      time.Time      `json:"created_at"`
}

type ExecutorNode struct {
	ID                         string         `json:"id"`
	OrgID                      string         `json:"org_id,omitempty"`
	Kind                       string         `json:"kind"`
	Name                       string         `json:"name"`
	Address                    string         `json:"address,omitempty"`
	AgentDURL                  string         `json:"agentd_url,omitempty"`
	HostKeyFingerprint         string         `json:"host_key_fingerprint,omitempty"`
	ObservedHostKeyFingerprint string         `json:"observed_host_key_fingerprint,omitempty"`
	HostKeyVerified            bool           `json:"host_key_verified"`
	ForcedCommand              string         `json:"forced_command,omitempty"`
	Labels                     map[string]any `json:"labels"`
	Capacity                   map[string]any `json:"capacity"`
	Status                     string         `json:"status"`
	LastSeenAt                 *time.Time     `json:"last_seen_at,omitempty"`
	VerifiedAt                 *time.Time     `json:"verified_at,omitempty"`
	CreatedAt                  time.Time      `json:"created_at"`
}

type TaskPolicy struct {
	AllowPush              bool `json:"allow_push"`
	AllowDependencyChange  bool `json:"allow_dependency_change"`
	AllowInfraChange       bool `json:"allow_infra_change"`
	RequireAudit           bool `json:"require_audit"`
	RequireTests           bool `json:"require_tests"`
	RequireHumanBeforePR   bool `json:"require_human_before_pr"`
	RequireHumanBeforePush bool `json:"require_human_before_push"`
}

type TaskEnvelope struct {
	TaskID             string     `json:"task_id"`
	ProjectID          string     `json:"project_id"`
	RepositoryID       string     `json:"repository_id"`
	Title              string     `json:"title"`
	BaseBranch         string     `json:"base_branch"`
	TargetBranch       string     `json:"target_branch"`
	Role               string     `json:"role"`
	Skill              string     `json:"skill"`
	AgentProfile       string     `json:"agent_profile"`
	Executor           string     `json:"executor"`
	AllowedPaths       []string   `json:"allowed_paths"`
	ForbiddenPaths     []string   `json:"forbidden_paths"`
	AllowedCommands    []string   `json:"allowed_commands"`
	Network            bool       `json:"network"`
	Objective          string     `json:"objective"`
	AcceptanceCriteria []string   `json:"acceptance_criteria"`
	StopConditions     []string   `json:"stop_conditions"`
	RequiredOutputs    []string   `json:"required_outputs"`
	Policy             TaskPolicy `json:"policy"`
}

type Task struct {
	ID           string       `json:"id"`
	ProjectID    string       `json:"project_id"`
	RepositoryID string       `json:"repository_id"`
	TaskKey      string       `json:"task_key"`
	Title        string       `json:"title"`
	Status       string       `json:"status"`
	Envelope     TaskEnvelope `json:"envelope"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

type Run struct {
	ID             string         `json:"id"`
	TaskID         string         `json:"task_id"`
	Role           string         `json:"role"`
	Status         string         `json:"status"`
	Executor       string         `json:"executor"`
	ExecutorNodeID string         `json:"executor_node_id,omitempty"`
	Branch         string         `json:"branch,omitempty"`
	WorktreePath   string         `json:"worktree_path,omitempty"`
	Result         map[string]any `json:"result"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type RunEvent struct {
	ID        int64          `json:"id"`
	RunID     string         `json:"run_id"`
	Seq       int64          `json:"seq"`
	Level     string         `json:"level"`
	EventType string         `json:"event_type"`
	Message   string         `json:"message"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type Artifact struct {
	ID        string         `json:"id"`
	RunID     string         `json:"run_id"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	SHA256    string         `json:"sha256,omitempty"`
	SizeBytes int64          `json:"size_bytes,omitempty"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

type Approval struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	ApprovalType string     `json:"approval_type"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason"`
	RequestedBy  string     `json:"requested_by,omitempty"`
	ApprovedBy   string     `json:"approved_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
}

type ScopeCheckRecord struct {
	ID        string           `json:"id"`
	TaskID    string           `json:"task_id"`
	RunID     string           `json:"run_id,omitempty"`
	BaseRef   string           `json:"base_ref"`
	Result    ScopeCheckResult `json:"result"`
	CreatedAt time.Time        `json:"created_at"`
}

type WorkflowState struct {
	Task             Task              `json:"task"`
	Runs             []Run             `json:"runs"`
	LatestScopeCheck *ScopeCheckRecord `json:"latest_scope_check,omitempty"`
	Approvals        []Approval        `json:"approvals"`
	BlockedReasons   []string          `json:"blocked_reasons"`
	NextActions      []string          `json:"next_actions"`
	ReadyForPR       bool              `json:"ready_for_pr"`
}

type ToolCall struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id,omitempty"`
	Caller     string         `json:"caller"`
	ToolName   string         `json:"tool_name"`
	Input      map[string]any `json:"input"`
	Output     map[string]any `json:"output"`
	Status     string         `json:"status"`
	CreatedAt  time.Time      `json:"created_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

type MCPSession struct {
	ID              string    `json:"id"`
	ActorID         string    `json:"actor_id"`
	ProtocolVersion string    `json:"protocol_version"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	LastEventID     int64     `json:"last_event_id"`
}

type MCPSessionEvent struct {
	ID        string         `json:"id"`
	SessionID string         `json:"session_id"`
	Seq       int64          `json:"seq"`
	EventType string         `json:"event_type"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type MCPSessionEventNotification struct {
	SessionID string `json:"session_id"`
	Seq       int64  `json:"seq"`
	EventType string `json:"event_type"`
}

type MCPSessionRetentionResult struct {
	DryRun          bool      `json:"dry_run"`
	Cutoff          time.Time `json:"cutoff"`
	ScannedSessions int64     `json:"scanned_sessions"`
	DeletedSessions int64     `json:"deleted_sessions"`
	DeletedEvents   int64     `json:"deleted_events"`
}

type AuthTokenRevocation struct {
	ID        string    `json:"id"`
	TokenHash string    `json:"token_hash"`
	ActorID   string    `json:"actor_id"`
	Subject   string    `json:"subject"`
	Reason    string    `json:"reason"`
	RevokedAt time.Time `json:"revoked_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AuthTokenRevocationRetentionResult struct {
	DryRun  bool      `json:"dry_run"`
	Cutoff  time.Time `json:"cutoff"`
	Scanned int64     `json:"scanned"`
	Deleted int64     `json:"deleted"`
}

type AuthSession struct {
	ID                string     `json:"id"`
	TokenHash         string     `json:"token_hash"`
	UserID            string     `json:"user_id"`
	Provider          string     `json:"provider"`
	Subject           string     `json:"subject"`
	ExternalSessionID string     `json:"external_session_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	ExpiresAt         time.Time  `json:"expires_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

type AuthSessionRetentionResult struct {
	DryRun  bool      `json:"dry_run"`
	Cutoff  time.Time `json:"cutoff"`
	Scanned int64     `json:"scanned"`
	Deleted int64     `json:"deleted"`
}

type AuthLoginState struct {
	ID           string     `json:"id"`
	StateHash    string     `json:"state_hash"`
	NonceHash    string     `json:"nonce_hash"`
	CodeVerifier string     `json:"-"`
	ReturnTo     string     `json:"return_to"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	ConsumedAt   *time.Time `json:"consumed_at,omitempty"`
}

type AuthLoginStateRetentionResult struct {
	DryRun  bool      `json:"dry_run"`
	Cutoff  time.Time `json:"cutoff"`
	Scanned int64     `json:"scanned"`
	Deleted int64     `json:"deleted"`
}

type AuditLog struct {
	ID           string         `json:"id"`
	OrgID        string         `json:"org_id,omitempty"`
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Payload      map[string]any `json:"payload"`
	PrevHash     string         `json:"prev_hash,omitempty"`
	EntryHash    string         `json:"entry_hash,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type ValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type ScopeCheckResult struct {
	Status       string   `json:"status"`
	ChangedFiles []string `json:"changed_files"`
	Violations   []string `json:"violations"`
}

func NewID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
