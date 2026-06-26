package store

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

var ErrNotFound = errors.New("not found")
var ErrNoCapacity = errors.New("no executor capacity available")
var ErrConflict = errors.New("resource already exists")

type MemoryStore struct {
	mu            sync.RWMutex
	auth          domain.AuthContext
	externalUsers map[string]domain.AuthContext
	revokedTokens map[string]domain.AuthTokenRevocation
	authSessions  map[string]domain.AuthSession
	sessionAuth   map[string]domain.AuthContext
	loginStates   map[string]domain.AuthLoginState
	organizations map[string]domain.Organization
	projects      map[string]domain.Project
	repositories  map[string]domain.Repository
	skills        map[string]domain.Skill
	skillVersions map[string][]domain.SkillVersion
	profiles      map[string]domain.AgentProfile
	nodes         map[string]domain.ExecutorNode
	tasks         map[string]domain.Task
	runs          map[string]domain.Run
	events        map[string][]domain.RunEvent
	artifacts     map[string]domain.Artifact
	scopeChecks   map[string][]domain.ScopeCheckRecord
	approvals     map[string]domain.Approval
	toolCalls     []domain.ToolCall
	mcpSessions   map[string]domain.MCPSession
	mcpEvents     map[string][]domain.MCPSessionEvent
	auditLogs     []domain.AuditLog
	nextEventID   int64
}

func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		projects:      map[string]domain.Project{},
		externalUsers: map[string]domain.AuthContext{},
		revokedTokens: map[string]domain.AuthTokenRevocation{},
		authSessions:  map[string]domain.AuthSession{},
		sessionAuth:   map[string]domain.AuthContext{},
		loginStates:   map[string]domain.AuthLoginState{},
		organizations: map[string]domain.Organization{},
		repositories:  map[string]domain.Repository{},
		skills:        map[string]domain.Skill{},
		skillVersions: map[string][]domain.SkillVersion{},
		profiles:      map[string]domain.AgentProfile{},
		nodes:         map[string]domain.ExecutorNode{},
		tasks:         map[string]domain.Task{},
		runs:          map[string]domain.Run{},
		events:        map[string][]domain.RunEvent{},
		artifacts:     map[string]domain.Artifact{},
		scopeChecks:   map[string][]domain.ScopeCheckRecord{},
		approvals:     map[string]domain.Approval{},
		toolCalls:     []domain.ToolCall{},
		mcpSessions:   map[string]domain.MCPSession{},
		mcpEvents:     map[string][]domain.MCPSessionEvent{},
		auditLogs:     []domain.AuditLog{},
		nextEventID:   1,
	}
	s.seed()
	return s
}

func (s *MemoryStore) seed() {
	now := time.Now().UTC()
	user := domain.User{
		ID:          "user_local_dev",
		Email:       "local-dev@multi-codex.invalid",
		DisplayName: "Local Developer",
		CreatedAt:   now,
	}
	membership := domain.Membership{
		OrgID:     "org_default",
		UserID:    user.ID,
		Role:      "owner",
		CreatedAt: now,
	}
	s.auth = domain.AuthContext{
		User:        user,
		Membership:  membership,
		Permissions: []string{"*"},
	}
	org := domain.Organization{
		ID:        membership.OrgID,
		Name:      "Default Organization",
		Slug:      "default",
		CreatedAt: now,
	}
	project := domain.Project{
		ID:          "proj_demo",
		Name:        "Demo Engineering",
		Slug:        "demo-engineering",
		Description: "Seed project for local multi-codex development.",
		CreatedAt:   now,
	}
	repo := domain.Repository{
		ID:            "repo_demo",
		ProjectID:     project.ID,
		Name:          "demo-service",
		Provider:      "local",
		RemoteURL:     "file:///workspace/demo-service.git",
		DefaultBranch: "main",
		CreatedAt:     now,
	}
	s.organizations[org.ID] = org
	s.projects[project.ID] = project
	s.repositories[repo.ID] = repo
	for _, seed := range seedSkillVersions(now) {
		s.skills[seed.Skill.ID] = seed.Skill
		s.skillVersions[seed.Skill.ID] = append(s.skillVersions[seed.Skill.ID], seed.Version)
	}
	for _, profile := range seedProfiles(project.ID, now) {
		s.profiles[profile.ID] = profile
	}
	for _, node := range seedNodes(now) {
		s.nodes[node.ID] = node
	}
}

func (s *MemoryStore) GetAuthContext() domain.AuthContext {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.auth
}

func (s *MemoryStore) UpsertExternalUser(provider string, subject string, email string, displayName string, role string, orgID string) (domain.AuthContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if provider == "" {
		provider = "oidc"
	}
	if subject == "" {
		return domain.AuthContext{}, errors.New("external subject is required")
	}
	if email == "" {
		email = subject + "@oidc.multi-codex.invalid"
	}
	if displayName == "" {
		displayName = subject
	}
	if role == "" {
		role = "viewer"
	}
	if orgID == "" {
		orgID = s.auth.Membership.OrgID
	}
	now := time.Now().UTC()
	key := provider + ":" + subject
	existing, ok := s.externalUsers[key]
	userID := existing.User.ID
	createdAt := existing.User.CreatedAt
	if !ok {
		userID = domain.NewID("user")
		createdAt = now
	}
	auth := domain.AuthContext{
		User: domain.User{
			ID:          userID,
			Email:       email,
			DisplayName: displayName,
			CreatedAt:   createdAt,
		},
		Membership: domain.Membership{
			OrgID:     orgID,
			UserID:    userID,
			Role:      role,
			CreatedAt: now,
		},
		Permissions: PermissionsForRole(role),
	}
	s.externalUsers[key] = auth
	return auth, nil
}

func (s *MemoryStore) RevokeAuthToken(revocation domain.AuthTokenRevocation) (domain.AuthTokenRevocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if revocation.TokenHash == "" {
		return domain.AuthTokenRevocation{}, errors.New("token hash is required")
	}
	if revocation.ID == "" {
		revocation.ID = domain.NewID("auth_revocation")
	}
	if revocation.Reason == "" {
		revocation.Reason = "logout"
	}
	if revocation.RevokedAt.IsZero() {
		revocation.RevokedAt = time.Now().UTC()
	}
	if revocation.ExpiresAt.IsZero() {
		revocation.ExpiresAt = revocation.RevokedAt.Add(24 * time.Hour)
	}
	if existing, ok := s.revokedTokens[revocation.TokenHash]; ok {
		return existing, nil
	}
	s.revokedTokens[revocation.TokenHash] = revocation
	return revocation, nil
}

func (s *MemoryStore) IsAuthTokenRevoked(tokenHash string, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	revocation, ok := s.revokedTokens[tokenHash]
	if !ok {
		return false
	}
	return revocation.ExpiresAt.IsZero() || revocation.ExpiresAt.After(now)
}

func (s *MemoryStore) CleanupAuthTokenRevocations(cutoff time.Time, dryRun bool) (domain.AuthTokenRevocationRetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := domain.AuthTokenRevocationRetentionResult{DryRun: dryRun, Cutoff: cutoff}
	for hash, revocation := range s.revokedTokens {
		if revocation.ExpiresAt.IsZero() || !revocation.ExpiresAt.Before(cutoff) {
			continue
		}
		result.Scanned++
		result.Deleted++
		if !dryRun {
			delete(s.revokedTokens, hash)
		}
	}
	return result, nil
}

func (s *MemoryStore) CreateAuthSession(tokenHash string, auth domain.AuthContext, provider string, subject string, expiresAt time.Time) (domain.AuthSession, error) {
	return s.CreateAuthSessionWithExternalID(tokenHash, auth, provider, subject, "", expiresAt)
}

func (s *MemoryStore) CreateAuthSessionWithExternalID(tokenHash string, auth domain.AuthContext, provider string, subject string, externalSessionID string, expiresAt time.Time) (domain.AuthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tokenHash == "" {
		return domain.AuthSession{}, errors.New("session hash is required")
	}
	now := time.Now().UTC()
	session := domain.AuthSession{
		ID:                domain.NewID("auth_session"),
		TokenHash:         tokenHash,
		UserID:            auth.User.ID,
		Provider:          provider,
		Subject:           subject,
		ExternalSessionID: externalSessionID,
		CreatedAt:         now,
		LastSeenAt:        now,
		ExpiresAt:         expiresAt,
	}
	s.authSessions[tokenHash] = session
	s.sessionAuth[tokenHash] = auth
	return session, nil
}

func (s *MemoryStore) GetAuthSession(tokenHash string, now time.Time) (domain.AuthContext, domain.AuthSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.authSessions[tokenHash]
	if !ok {
		return domain.AuthContext{}, domain.AuthSession{}, ErrNotFound
	}
	if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return domain.AuthContext{}, session, ErrNotFound
	}
	session.LastSeenAt = now
	s.authSessions[tokenHash] = session
	auth := s.sessionAuth[tokenHash]
	return auth, session, nil
}

func (s *MemoryStore) RevokeAuthSession(tokenHash string, revokedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.authSessions[tokenHash]
	if !ok {
		return nil
	}
	session.RevokedAt = &revokedAt
	s.authSessions[tokenHash] = session
	return nil
}

func (s *MemoryStore) RevokeAuthSessionsByExternalSessionID(provider string, externalSessionID string, revokedAt time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if externalSessionID == "" {
		return 0, nil
	}
	var revoked int64
	for hash, session := range s.authSessions {
		if session.Provider != provider || session.ExternalSessionID != externalSessionID || session.RevokedAt != nil {
			continue
		}
		session.RevokedAt = &revokedAt
		s.authSessions[hash] = session
		revoked++
	}
	return revoked, nil
}

func (s *MemoryStore) RevokeAuthSessionsBySubject(provider string, subject string, revokedAt time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if subject == "" {
		return 0, nil
	}
	var revoked int64
	for hash, session := range s.authSessions {
		if session.Provider != provider || session.Subject != subject || session.RevokedAt != nil {
			continue
		}
		session.RevokedAt = &revokedAt
		s.authSessions[hash] = session
		revoked++
	}
	return revoked, nil
}

func (s *MemoryStore) CleanupAuthSessions(cutoff time.Time, dryRun bool) (domain.AuthSessionRetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := domain.AuthSessionRetentionResult{DryRun: dryRun, Cutoff: cutoff}
	for hash, session := range s.authSessions {
		revokedExpired := session.RevokedAt != nil && session.RevokedAt.Before(cutoff)
		sessionExpired := session.ExpiresAt.Before(cutoff)
		if !revokedExpired && !sessionExpired {
			continue
		}
		result.Scanned++
		result.Deleted++
		if !dryRun {
			delete(s.authSessions, hash)
			delete(s.sessionAuth, hash)
		}
	}
	return result, nil
}

func (s *MemoryStore) CreateAuthLoginState(state domain.AuthLoginState) (domain.AuthLoginState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if state.StateHash == "" {
		return domain.AuthLoginState{}, errors.New("state hash is required")
	}
	if state.NonceHash == "" {
		return domain.AuthLoginState{}, errors.New("nonce hash is required")
	}
	if state.CodeVerifier == "" {
		return domain.AuthLoginState{}, errors.New("code verifier is required")
	}
	now := time.Now().UTC()
	if state.ID == "" {
		state.ID = domain.NewID("auth_login_state")
	}
	if state.CreatedAt.IsZero() {
		state.CreatedAt = now
	}
	if state.ExpiresAt.IsZero() {
		state.ExpiresAt = now.Add(10 * time.Minute)
	}
	if state.ReturnTo == "" {
		state.ReturnTo = "/"
	}
	s.loginStates[state.StateHash] = state
	return state, nil
}

func (s *MemoryStore) ConsumeAuthLoginState(stateHash string, now time.Time) (domain.AuthLoginState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.loginStates[stateHash]
	if !ok || state.ConsumedAt != nil || !state.ExpiresAt.After(now) {
		return domain.AuthLoginState{}, ErrNotFound
	}
	consumedAt := now.UTC()
	state.ConsumedAt = &consumedAt
	s.loginStates[stateHash] = state
	return state, nil
}

func (s *MemoryStore) CleanupAuthLoginStates(cutoff time.Time, dryRun bool) (domain.AuthLoginStateRetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := domain.AuthLoginStateRetentionResult{DryRun: dryRun, Cutoff: cutoff}
	for hash, state := range s.loginStates {
		consumedExpired := state.ConsumedAt != nil && state.ConsumedAt.Before(cutoff)
		stateExpired := state.ExpiresAt.Before(cutoff)
		if !consumedExpired && !stateExpired {
			continue
		}
		result.Scanned++
		result.Deleted++
		if !dryRun {
			delete(s.loginStates, hash)
		}
	}
	return result, nil
}

func (s *MemoryStore) ListOrganizations() []domain.Organization {
	s.mu.RLock()
	defer s.mu.RUnlock()

	orgs := make([]domain.Organization, 0, len(s.organizations))
	for _, org := range s.organizations {
		orgs = append(orgs, org)
	}
	sort.Slice(orgs, func(i, j int) bool { return orgs[i].CreatedAt.Before(orgs[j].CreatedAt) })
	return orgs
}

func (s *MemoryStore) CreateOrganization(org domain.Organization) (domain.Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.organizations {
		if existing.Slug == org.Slug {
			return domain.Organization{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	if org.ID == "" {
		org.ID = domain.NewID("org")
	}
	org.CreatedAt = now
	s.organizations[org.ID] = org
	return org, nil
}

func (s *MemoryStore) ListProjects() []domain.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	projects := make([]domain.Project, 0, len(s.projects))
	for _, project := range s.projects {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].CreatedAt.Before(projects[j].CreatedAt) })
	return projects
}

func (s *MemoryStore) CreateProject(project domain.Project) domain.Project {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if project.ID == "" {
		project.ID = domain.NewID("proj")
	}
	project.CreatedAt = now
	s.projects[project.ID] = project
	return project
}

func (s *MemoryStore) GetProject(id string) (domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.projects[id]
	if !ok {
		return domain.Project{}, ErrNotFound
	}
	return project, nil
}

func (s *MemoryStore) ListRepositories(projectID string) []domain.Repository {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repos := []domain.Repository{}
	for _, repo := range s.repositories {
		if repo.ProjectID == projectID {
			repos = append(repos, repo)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].CreatedAt.Before(repos[j].CreatedAt) })
	return repos
}

func (s *MemoryStore) CreateRepository(repo domain.Repository) domain.Repository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if repo.ID == "" {
		repo.ID = domain.NewID("repo")
	}
	if repo.DefaultBranch == "" {
		repo.DefaultBranch = "main"
	}
	repo.CreatedAt = time.Now().UTC()
	s.repositories[repo.ID] = repo
	return repo
}

func (s *MemoryStore) GetRepository(id string) (domain.Repository, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	repo, ok := s.repositories[id]
	if !ok {
		return domain.Repository{}, ErrNotFound
	}
	return repo, nil
}

func (s *MemoryStore) ListSkills() []domain.Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()

	skills := make([]domain.Skill, 0, len(s.skills))
	for _, skill := range s.skills {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

func (s *MemoryStore) ListSkillVersions(skillID string) []domain.SkillVersion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions := append([]domain.SkillVersion(nil), s.skillVersions[skillID]...)
	sort.Slice(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
	return versions
}

func (s *MemoryStore) CreateSkill(skill domain.Skill, version domain.SkillVersion) (domain.Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if skill.ID == "" {
		for _, existing := range s.skills {
			if existing.Name == skill.Name {
				skill.ID = existing.ID
				skill.CreatedAt = existing.CreatedAt
				break
			}
		}
		if skill.ID == "" {
			skill.ID = domain.NewID("skill")
		}
	}
	if existing, ok := s.skills[skill.ID]; ok && skill.CreatedAt.IsZero() {
		skill.CreatedAt = existing.CreatedAt
	}
	if skill.Role == "" {
		skill.Role = "feature"
	}
	if version.Version == "" {
		version.Version = "local"
	}
	if version.Path == "" {
		version.Path = "skills/" + skill.Name + "/SKILL.md"
	}
	if version.ContentHash == "" {
		version.ContentHash = deterministicHash(skill.Name + ":" + version.Path + ":" + version.Version)
	}
	if version.ID == "" {
		version.ID = domain.NewID("skill_version")
	}
	version.SkillID = skill.ID
	version.CreatedAt = now
	skill.Enabled = true
	skill.LatestVersion = version.Version
	skill.ContentHash = version.ContentHash
	skill.Path = version.Path
	if skill.CreatedAt.IsZero() {
		skill.CreatedAt = now
	}
	s.skills[skill.ID] = skill
	s.skillVersions[skill.ID] = upsertSkillVersion(s.skillVersions[skill.ID], version)
	return skill, nil
}

func upsertSkillVersion(versions []domain.SkillVersion, next domain.SkillVersion) []domain.SkillVersion {
	for i, version := range versions {
		if version.Version == next.Version {
			versions[i] = next
			return versions
		}
	}
	return append(versions, next)
}

func (s *MemoryStore) ListAgentProfiles(projectID string) []domain.AgentProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	profiles := []domain.AgentProfile{}
	for _, profile := range s.profiles {
		if projectID == "" || profile.ProjectID == "" || profile.ProjectID == projectID {
			profiles = append(profiles, profile)
		}
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
}

func (s *MemoryStore) CreateAgentProfile(profile domain.AgentProfile) (domain.AgentProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if profile.ID == "" {
		profile.ID = domain.NewID("profile")
	}
	if profile.Config == nil {
		profile.Config = map[string]any{}
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = time.Now().UTC()
	}
	s.profiles[profile.ID] = profile
	return profile, nil
}

func (s *MemoryStore) ListExecutorNodes() []domain.ExecutorNode {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nodes := make([]domain.ExecutorNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes
}

func (s *MemoryStore) GetExecutorNode(id string) (domain.ExecutorNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[id]
	if !ok {
		return domain.ExecutorNode{}, ErrNotFound
	}
	return node, nil
}

func (s *MemoryStore) RegisterExecutorNode(node domain.ExecutorNode) (domain.ExecutorNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node.ID == "" {
		node.ID = domain.NewID("node")
	}
	if node.Status == "" {
		node.Status = "active"
	}
	if node.Labels == nil {
		node.Labels = map[string]any{}
	}
	if node.Capacity == nil {
		node.Capacity = map[string]any{}
	}
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now().UTC()
	}
	s.nodes[node.ID] = node
	return node, nil
}

func (s *MemoryStore) VerifyExecutorNodeHostKey(id string, observedFingerprint string) (domain.ExecutorNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[id]
	if !ok {
		return domain.ExecutorNode{}, ErrNotFound
	}
	if node.HostKeyFingerprint == "" {
		node.HostKeyFingerprint = observedFingerprint
	}
	node.ObservedHostKeyFingerprint = observedFingerprint
	node.HostKeyVerified = node.HostKeyFingerprint == observedFingerprint
	now := time.Now().UTC()
	if node.HostKeyVerified {
		node.Status = "active"
		node.VerifiedAt = &now
	}
	s.nodes[id] = node
	return node, nil
}

func (s *MemoryStore) CreateTask(envelope domain.TaskEnvelope) domain.Task {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	task := domain.Task{
		ID:           domain.NewID("task"),
		ProjectID:    envelope.ProjectID,
		RepositoryID: envelope.RepositoryID,
		TaskKey:      envelope.TaskID,
		Title:        envelope.Title,
		Status:       "draft",
		Envelope:     envelope,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.tasks[task.ID] = task
	return task
}

func (s *MemoryStore) GetTask(id string) (domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return domain.Task{}, ErrNotFound
	}
	return task, nil
}

func (s *MemoryStore) ListTasks(projectID string) []domain.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tasks := []domain.Task{}
	for _, task := range s.tasks {
		if task.ProjectID == projectID {
			tasks = append(tasks, task)
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.Before(tasks[j].CreatedAt) })
	return tasks
}

func (s *MemoryStore) UpdateTaskStatus(taskID string, status string) (domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return domain.Task{}, ErrNotFound
	}
	task.Status = status
	task.UpdatedAt = time.Now().UTC()
	s.tasks[taskID] = task
	return task, nil
}

func (s *MemoryStore) StartRun(taskID string, role string, executor string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	now := time.Now().UTC()
	nodeID := s.selectExecutorNodeLocked(executor)
	if nodeID == "" {
		return domain.Run{}, ErrNoCapacity
	}
	run := domain.Run{
		ID:             domain.NewID("run"),
		TaskID:         taskID,
		Role:           role,
		Status:         "running",
		Executor:       executor,
		ExecutorNodeID: nodeID,
		Result:         map[string]any{},
		StartedAt:      &now,
		CreatedAt:      now,
	}
	s.runs[run.ID] = run
	task.Status = "running"
	task.UpdatedAt = now
	s.tasks[taskID] = task
	s.appendEventLocked(run.ID, "info", "worker_spawn", "Worker run was scheduled by the API", map[string]any{
		"role":             role,
		"executor":         executor,
		"executor_node_id": nodeID,
	})
	return run, nil
}

func (s *MemoryStore) EnqueueRun(taskID string, role string, executor string, priority int, attempt int, maxAttempts int, reason string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	now := time.Now().UTC()
	run := domain.Run{
		ID:        domain.NewID("run"),
		TaskID:    taskID,
		Role:      role,
		Status:    "queued",
		Executor:  executor,
		Result:    queueResult(priority, attempt, maxAttempts, reason),
		CreatedAt: now,
	}
	s.runs[run.ID] = run
	task.Status = "queued"
	task.UpdatedAt = now
	s.tasks[taskID] = task
	s.appendEventLocked(run.ID, "info", "worker_queued", "Worker run was queued", map[string]any{
		"role":         role,
		"executor":     executor,
		"priority":     priority,
		"attempt":      attempt,
		"max_attempts": maxAttempts,
		"reason":       reason,
	})
	return run, nil
}

func (s *MemoryStore) DispatchQueuedRun() (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	queued := make([]domain.Run, 0)
	for _, run := range s.runs {
		if run.Status == "queued" {
			queued = append(queued, run)
		}
	}
	if len(queued) == 0 {
		return domain.Run{}, ErrNotFound
	}
	sort.Slice(queued, func(i, j int) bool {
		leftPriority := intFromMap(queued[i].Result, "queue_priority", 0)
		rightPriority := intFromMap(queued[j].Result, "queue_priority", 0)
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		return queued[i].CreatedAt.Before(queued[j].CreatedAt)
	})
	for _, candidate := range queued {
		nodeID := s.selectExecutorNodeLocked(candidate.Executor)
		if nodeID == "" {
			continue
		}
		now := time.Now().UTC()
		candidate.Status = "running"
		candidate.ExecutorNodeID = nodeID
		candidate.StartedAt = &now
		s.runs[candidate.ID] = candidate
		if task, ok := s.tasks[candidate.TaskID]; ok {
			task.Status = "running"
			task.UpdatedAt = now
			s.tasks[candidate.TaskID] = task
		}
		s.appendEventLocked(candidate.ID, "info", "worker_spawn", "Queued worker run was dispatched", map[string]any{
			"role":             candidate.Role,
			"executor":         candidate.Executor,
			"executor_node_id": nodeID,
			"priority":         intFromMap(candidate.Result, "queue_priority", 0),
			"attempt":          intFromMap(candidate.Result, "retry_attempt", 1),
			"max_attempts":     intFromMap(candidate.Result, "max_attempts", 1),
		})
		return candidate, nil
	}
	return domain.Run{}, ErrNoCapacity
}

func (s *MemoryStore) selectExecutorNodeLocked(executor string) string {
	nodes := make([]domain.ExecutorNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node.Kind != executor || node.Status != "active" {
			continue
		}
		if executor == "ssh" && !node.HostKeyVerified {
			continue
		}
		if s.activeRunCountForNodeLocked(node.ID) >= concurrencyForNode(node) {
			continue
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return ""
	}
	sort.Slice(nodes, func(i, j int) bool {
		leftActive := s.activeRunCountForNodeLocked(nodes[i].ID)
		rightActive := s.activeRunCountForNodeLocked(nodes[j].ID)
		leftConcurrency := concurrencyForNode(nodes[i])
		rightConcurrency := concurrencyForNode(nodes[j])
		leftUtilization := float64(leftActive) / float64(leftConcurrency)
		rightUtilization := float64(rightActive) / float64(rightConcurrency)
		if leftUtilization != rightUtilization {
			return leftUtilization < rightUtilization
		}
		leftAvailable := leftConcurrency - leftActive
		rightAvailable := rightConcurrency - rightActive
		if leftAvailable != rightAvailable {
			return leftAvailable > rightAvailable
		}
		if nodes[i].LastSeenAt != nil && nodes[j].LastSeenAt != nil {
			return nodes[i].LastSeenAt.After(*nodes[j].LastSeenAt)
		}
		if nodes[i].LastSeenAt != nil {
			return true
		}
		if nodes[j].LastSeenAt != nil {
			return false
		}
		return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
	})
	return nodes[0].ID
}

func (s *MemoryStore) activeRunCountForNodeLocked(nodeID string) int {
	count := 0
	for _, run := range s.runs {
		if run.ExecutorNodeID != nodeID {
			continue
		}
		switch run.Status {
		case "queued", "preparing", "running":
			count++
		}
	}
	return count
}

func concurrencyForNode(node domain.ExecutorNode) int {
	value, ok := node.Capacity["concurrency"]
	if !ok {
		return 1
	}
	switch typed := value.(type) {
	case int:
		if typed > 0 {
			return typed
		}
	case int64:
		if typed > 0 {
			return int(typed)
		}
	case float64:
		if typed > 0 {
			return int(typed)
		}
	}
	return 1
}

func (s *MemoryStore) GetRun(id string) (domain.Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[id]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	return run, nil
}

func (s *MemoryStore) UpdateRunWorkspace(runID string, branch string, worktreePath string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	run.Branch = branch
	run.WorktreePath = worktreePath
	s.runs[runID] = run
	return run, nil
}

func (s *MemoryStore) ListAllRuns() []domain.Run {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runs := make([]domain.Run, 0, len(s.runs))
	for _, run := range s.runs {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs
}

func (s *MemoryStore) ListRuns(taskID string) []domain.Run {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runs := []domain.Run{}
	for _, run := range s.runs {
		if run.TaskID == taskID {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	return runs
}

func (s *MemoryStore) FinishRun(runID string, status string, result map[string]any) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return domain.Run{}, ErrNotFound
	}
	now := time.Now().UTC()
	run.Status = status
	run.Result = result
	run.FinishedAt = &now
	s.runs[runID] = run
	s.appendEventLocked(runID, "info", "worker_result", "Worker result was recorded", map[string]any{
		"status": status,
		"result": result,
	})

	if task, ok := s.tasks[run.TaskID]; ok {
		switch status {
		case "succeeded":
			if run.Role == "git_sync" || run.Role == "git-sync" {
				task.Status = "completed"
			} else {
				task.Status = "running"
			}
		case "blocked":
			task.Status = "blocked"
		case "failed", "timed_out", "cancelled":
			task.Status = "failed"
		}
		task.UpdatedAt = now
		s.tasks[task.ID] = task
	}
	return run, nil
}

func (s *MemoryStore) ListEvents(runID string) []domain.RunEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := append([]domain.RunEvent(nil), s.events[runID]...)
	sort.Slice(events, func(i, j int) bool { return events[i].Seq < events[j].Seq })
	return events
}

func (s *MemoryStore) AddEvent(runID string, level string, eventType string, message string, payload map[string]any) (domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.runs[runID]; !ok {
		return domain.RunEvent{}, ErrNotFound
	}
	return s.appendEventLocked(runID, level, eventType, message, payload), nil
}

func (s *MemoryStore) CreateArtifact(artifact domain.Artifact) (domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.runs[artifact.RunID]; !ok {
		return domain.Artifact{}, ErrNotFound
	}
	if artifact.ID == "" {
		artifact.ID = domain.NewID("artifact")
	}
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	s.artifacts[artifact.ID] = artifact
	return artifact, nil
}

func (s *MemoryStore) ListArtifacts(runID string) []domain.Artifact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artifacts := []domain.Artifact{}
	for _, artifact := range s.artifacts {
		if artifact.RunID == runID {
			artifacts = append(artifacts, artifact)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt) })
	return artifacts
}

func (s *MemoryStore) GetArtifact(id string) (domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artifact, ok := s.artifacts[id]
	if !ok {
		return domain.Artifact{}, ErrNotFound
	}
	return artifact, nil
}

func (s *MemoryStore) RecordScopeCheck(taskID string, runID string, baseRef string, result domain.ScopeCheckResult) (domain.ScopeCheckRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[taskID]; !ok {
		return domain.ScopeCheckRecord{}, ErrNotFound
	}
	record := domain.ScopeCheckRecord{
		ID:        domain.NewID("scope"),
		TaskID:    taskID,
		RunID:     runID,
		BaseRef:   baseRef,
		Result:    result,
		CreatedAt: time.Now().UTC(),
	}
	s.scopeChecks[taskID] = append(s.scopeChecks[taskID], record)
	return record, nil
}

func (s *MemoryStore) LatestScopeCheck(taskID string) (domain.ScopeCheckRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := s.scopeChecks[taskID]
	if len(records) == 0 {
		return domain.ScopeCheckRecord{}, ErrNotFound
	}
	return records[len(records)-1], nil
}

func (s *MemoryStore) ListApprovals(taskID string) []domain.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()

	approvals := []domain.Approval{}
	for _, approval := range s.approvals {
		if taskID == "" || approval.TaskID == taskID {
			approvals = append(approvals, approval)
		}
	}
	sort.Slice(approvals, func(i, j int) bool { return approvals[i].CreatedAt.Before(approvals[j].CreatedAt) })
	return approvals
}

func (s *MemoryStore) CreateApproval(approval domain.Approval) (domain.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[approval.TaskID]; !ok {
		return domain.Approval{}, ErrNotFound
	}
	if approval.ID == "" {
		approval.ID = domain.NewID("approval")
	}
	if approval.Status == "" {
		approval.Status = "pending"
	}
	if approval.CreatedAt.IsZero() {
		approval.CreatedAt = time.Now().UTC()
	}
	s.approvals[approval.ID] = approval
	return approval, nil
}

func (s *MemoryStore) DecideApproval(approvalID string, status string, approvedBy string, reason string) (domain.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	approval, ok := s.approvals[approvalID]
	if !ok {
		return domain.Approval{}, ErrNotFound
	}
	now := time.Now().UTC()
	approval.Status = status
	approval.ApprovedBy = approvedBy
	if reason != "" {
		approval.Reason = reason
	}
	approval.DecidedAt = &now
	s.approvals[approvalID] = approval
	return approval, nil
}

func (s *MemoryStore) RecordToolCall(call domain.ToolCall) domain.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if call.ID == "" {
		call.ID = domain.NewID("tool")
	}
	if call.CreatedAt.IsZero() {
		call.CreatedAt = now
	}
	if call.FinishedAt == nil {
		call.FinishedAt = &now
	}
	if call.Input == nil {
		call.Input = map[string]any{}
	}
	if call.Output == nil {
		call.Output = map[string]any{}
	}
	s.toolCalls = append(s.toolCalls, call)
	return call
}

func (s *MemoryStore) ListToolCalls() []domain.ToolCall {
	s.mu.RLock()
	defer s.mu.RUnlock()

	calls := append([]domain.ToolCall(nil), s.toolCalls...)
	sort.Slice(calls, func(i, j int) bool { return calls[i].CreatedAt.After(calls[j].CreatedAt) })
	return calls
}

func (s *MemoryStore) UpsertMCPSession(session domain.MCPSession) (domain.MCPSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := s.mcpSessions[session.ID]
	if session.ID == "" {
		return domain.MCPSession{}, ErrInvalidID
	}
	if session.ProtocolVersion == "" {
		session.ProtocolVersion = "2025-06-18"
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if ok && !existing.CreatedAt.IsZero() {
		session.CreatedAt = existing.CreatedAt
	} else if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = now
	}
	if session.ExpiresAt.IsZero() {
		session.ExpiresAt = session.LastSeenAt
	}
	if ok && existing.LastEventID > session.LastEventID {
		session.LastEventID = existing.LastEventID
	}
	s.mcpSessions[session.ID] = session
	return session, nil
}

func (s *MemoryStore) GetMCPSession(id string) (domain.MCPSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.mcpSessions[id]
	if !ok {
		return domain.MCPSession{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) AppendMCPSessionEvent(sessionID string, eventType string, payload map[string]any) (domain.MCPSessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.mcpSessions[sessionID]
	if !ok {
		return domain.MCPSessionEvent{}, ErrNotFound
	}
	if payload == nil {
		payload = map[string]any{}
	}
	now := time.Now().UTC()
	session.LastEventID++
	session.LastSeenAt = now
	s.mcpSessions[sessionID] = session
	event := domain.MCPSessionEvent{
		ID:        domain.NewID("mcp_event"),
		SessionID: sessionID,
		Seq:       session.LastEventID,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: now,
	}
	s.mcpEvents[sessionID] = append(s.mcpEvents[sessionID], event)
	return event, nil
}

func (s *MemoryStore) ListMCPSessionEventsAfter(sessionID string, afterSeq int64, limit int) []domain.MCPSessionEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	events := []domain.MCPSessionEvent{}
	for _, event := range s.mcpEvents[sessionID] {
		if event.Seq > afterSeq {
			events = append(events, event)
			if len(events) >= limit {
				break
			}
		}
	}
	return events
}

func (s *MemoryStore) CleanupMCPSessions(cutoff time.Time, dryRun bool) (domain.MCPSessionRetentionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := domain.MCPSessionRetentionResult{DryRun: dryRun, Cutoff: cutoff}
	for id, session := range s.mcpSessions {
		if session.ExpiresAt.IsZero() || !session.ExpiresAt.Before(cutoff) {
			continue
		}
		result.ScannedSessions++
		result.DeletedSessions++
		result.DeletedEvents += int64(len(s.mcpEvents[id]))
		if !dryRun {
			delete(s.mcpSessions, id)
			delete(s.mcpEvents, id)
		}
	}
	return result, nil
}

func (s *MemoryStore) ListAuditLogs() []domain.AuditLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logs := append([]domain.AuditLog(nil), s.auditLogs...)
	sort.Slice(logs, func(i, j int) bool { return logs[i].CreatedAt.After(logs[j].CreatedAt) })
	return logs
}

func (s *MemoryStore) ListAuditLogsForSeal() []domain.AuditLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logs := append([]domain.AuditLog(nil), s.auditLogs...)
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].CreatedAt.Equal(logs[j].CreatedAt) {
			return logs[i].ID < logs[j].ID
		}
		return logs[i].CreatedAt.Before(logs[j].CreatedAt)
	})
	return logs
}

func (s *MemoryStore) RecordAuditLog(entry domain.AuditLog) domain.AuditLog {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.ID == "" {
		entry.ID = domain.NewID("audit")
	}
	prevHash := ""
	if len(s.auditLogs) > 0 {
		prevHash = s.auditLogs[len(s.auditLogs)-1].EntryHash
	}
	entry = prepareAuditEntry(entry, prevHash)
	s.auditLogs = append(s.auditLogs, entry)
	_ = exportAuditEntry(entry)
	return entry
}

func (s *MemoryStore) appendEventLocked(runID string, level string, eventType string, message string, payload map[string]any) domain.RunEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	event := domain.RunEvent{
		ID:        s.nextEventID,
		RunID:     runID,
		Seq:       int64(len(s.events[runID]) + 1),
		Level:     level,
		EventType: eventType,
		Message:   message,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	s.nextEventID++
	s.events[runID] = append(s.events[runID], event)
	return event
}
