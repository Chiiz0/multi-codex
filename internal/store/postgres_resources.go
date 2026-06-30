package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func (s *PostgresStore) GetAuthContext() domain.AuthContext {
	ctx, cancel := storeContext()
	defer cancel()

	var auth domain.AuthContext
	err := s.db.QueryRowContext(ctx, `
SELECT u.id::text, u.email, u.display_name, u.created_at,
       m.org_id::text, m.user_id::text, m.role, m.created_at
FROM users u
JOIN memberships m ON m.user_id = u.id
WHERE u.id = $1::uuid
LIMIT 1`, defaultUserID).Scan(
		&auth.User.ID, &auth.User.Email, &auth.User.DisplayName, &auth.User.CreatedAt,
		&auth.Membership.OrgID, &auth.Membership.UserID, &auth.Membership.Role, &auth.Membership.CreatedAt,
	)
	if err != nil {
		now := time.Now().UTC()
		auth.User = domain.User{ID: defaultUserID, Email: "local-dev@multi-codex.invalid", DisplayName: "Local Developer", CreatedAt: now}
		auth.Membership = domain.Membership{OrgID: defaultOrgID, UserID: defaultUserID, Role: "owner", CreatedAt: now}
	}
	auth.Permissions = permissionsForRole(auth.Membership.Role)
	return auth
}

func (s *PostgresStore) ListOrganizations() []domain.Organization {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, name, slug, created_at
FROM organizations
ORDER BY created_at ASC`)
	if err != nil {
		s.log.Error("list organizations failed", "error", err)
		return nil
	}
	defer rows.Close()

	orgs := []domain.Organization{}
	for rows.Next() {
		var org domain.Organization
		if err := rows.Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt); err != nil {
			s.log.Error("scan organization failed", "error", err)
			return orgs
		}
		orgs = append(orgs, org)
	}
	return orgs
}

func (s *PostgresStore) CreateOrganization(org domain.Organization) (domain.Organization, error) {
	ctx, cancel := storeContext()
	defer cancel()

	err := s.db.QueryRowContext(ctx, `
INSERT INTO organizations (name, slug)
VALUES ($1, $2)
ON CONFLICT (slug) DO NOTHING
RETURNING id::text, name, slug, created_at`,
		org.Name, org.Slug,
	).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Organization{}, ErrConflict
	}
	return org, err
}

func (s *PostgresStore) UpsertExternalUser(provider string, subject string, email string, displayName string, role string, orgID string) (domain.AuthContext, error) {
	ctx, cancel := storeContext()
	defer cancel()

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
		orgID = defaultOrgID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.AuthContext{}, err
	}
	defer tx.Rollback()

	var auth domain.AuthContext
	if err := tx.QueryRowContext(ctx, `
INSERT INTO users (email, display_name, external_provider, external_subject)
VALUES ($1, $2, $3, $4)
ON CONFLICT (external_provider, external_subject) WHERE external_subject IS NOT NULL
DO UPDATE SET email = EXCLUDED.email,
              display_name = EXCLUDED.display_name
RETURNING id::text, email, display_name, created_at`,
		email, displayName, provider, subject,
	).Scan(&auth.User.ID, &auth.User.Email, &auth.User.DisplayName, &auth.User.CreatedAt); err != nil {
		return domain.AuthContext{}, err
	}

	if err := tx.QueryRowContext(ctx, `
INSERT INTO memberships (org_id, user_id, role)
VALUES ($1::uuid, $2::uuid, $3)
ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role
RETURNING org_id::text, user_id::text, role, created_at`,
		orgID, auth.User.ID, role,
	).Scan(&auth.Membership.OrgID, &auth.Membership.UserID, &auth.Membership.Role, &auth.Membership.CreatedAt); err != nil {
		return domain.AuthContext{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.AuthContext{}, err
	}
	auth.Permissions = PermissionsForRole(auth.Membership.Role)
	return auth, nil
}

func (s *PostgresStore) RevokeAuthToken(revocation domain.AuthTokenRevocation) (domain.AuthTokenRevocation, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if revocation.TokenHash == "" {
		return domain.AuthTokenRevocation{}, errors.New("token hash is required")
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
	err := s.db.QueryRowContext(ctx, `
INSERT INTO auth_token_revocations (token_hash, actor_id, subject, reason, revoked_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (token_hash) DO UPDATE
SET actor_id = EXCLUDED.actor_id,
    subject = EXCLUDED.subject,
    reason = EXCLUDED.reason,
    expires_at = EXCLUDED.expires_at
RETURNING id::text, token_hash, actor_id, subject, reason, revoked_at, expires_at`,
		revocation.TokenHash, revocation.ActorID, revocation.Subject, revocation.Reason, revocation.RevokedAt, revocation.ExpiresAt,
	).Scan(&revocation.ID, &revocation.TokenHash, &revocation.ActorID, &revocation.Subject, &revocation.Reason, &revocation.RevokedAt, &revocation.ExpiresAt)
	return revocation, err
}

func (s *PostgresStore) IsAuthTokenRevoked(tokenHash string, now time.Time) bool {
	ctx, cancel := storeContext()
	defer cancel()

	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM auth_token_revocations
  WHERE token_hash = $1
    AND expires_at > $2
)`, tokenHash, now.UTC()).Scan(&exists)
	if err != nil {
		s.log.Error("check auth token revocation failed", "error", err)
		return false
	}
	return exists
}

func (s *PostgresStore) CleanupAuthTokenRevocations(cutoff time.Time, dryRun bool) (domain.AuthTokenRevocationRetentionResult, error) {
	ctx, cancel := storeContext()
	defer cancel()

	result := domain.AuthTokenRevocationRetentionResult{DryRun: dryRun, Cutoff: cutoff.UTC()}
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM auth_token_revocations
WHERE expires_at < $1`, result.Cutoff).Scan(&result.Scanned); err != nil {
		return result, err
	}
	result.Deleted = result.Scanned
	if dryRun || result.Scanned == 0 {
		return result, nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM auth_token_revocations
WHERE expires_at < $1`, result.Cutoff)
	return result, err
}

func (s *PostgresStore) CreateAuthSession(tokenHash string, auth domain.AuthContext, provider string, subject string, expiresAt time.Time) (domain.AuthSession, error) {
	return s.CreateAuthSessionWithExternalID(tokenHash, auth, provider, subject, "", expiresAt)
}

func (s *PostgresStore) CreateAuthSessionWithExternalID(tokenHash string, auth domain.AuthContext, provider string, subject string, externalSessionID string, expiresAt time.Time) (domain.AuthSession, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if tokenHash == "" {
		return domain.AuthSession{}, errors.New("session hash is required")
	}
	session := domain.AuthSession{TokenHash: tokenHash, UserID: auth.User.ID, Provider: provider, Subject: subject, ExternalSessionID: externalSessionID, ExpiresAt: expiresAt}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO auth_sessions (session_hash, user_id, external_provider, external_subject, external_session_id, expires_at)
VALUES ($1, $2::uuid, $3, $4, NULLIF($5, ''), $6)
RETURNING id::text, session_hash, user_id::text, external_provider, external_subject, COALESCE(external_session_id, ''), created_at, last_seen_at, expires_at`,
		tokenHash, auth.User.ID, provider, subject, externalSessionID, expiresAt,
	).Scan(&session.ID, &session.TokenHash, &session.UserID, &session.Provider, &session.Subject, &session.ExternalSessionID, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt)
	return session, err
}

func (s *PostgresStore) GetAuthSession(tokenHash string, now time.Time) (domain.AuthContext, domain.AuthSession, error) {
	ctx, cancel := storeContext()
	defer cancel()

	var auth domain.AuthContext
	var session domain.AuthSession
	var revoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `
SELECT s.id::text, s.session_hash, s.user_id::text, s.external_provider, s.external_subject, COALESCE(s.external_session_id, ''), s.created_at, s.last_seen_at, s.expires_at, s.revoked_at,
       u.id::text, u.email, u.display_name, u.created_at,
       m.org_id::text, m.user_id::text, m.role, m.created_at
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
JOIN memberships m ON m.user_id = u.id
WHERE s.session_hash = $1
  AND s.revoked_at IS NULL
	AND s.expires_at > $2
ORDER BY m.created_at ASC
LIMIT 1`, tokenHash, now.UTC()).Scan(
		&session.ID, &session.TokenHash, &session.UserID, &session.Provider, &session.Subject, &session.ExternalSessionID, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &revoked,
		&auth.User.ID, &auth.User.Email, &auth.User.DisplayName, &auth.User.CreatedAt,
		&auth.Membership.OrgID, &auth.Membership.UserID, &auth.Membership.Role, &auth.Membership.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AuthContext{}, domain.AuthSession{}, ErrNotFound
	}
	if err != nil {
		return domain.AuthContext{}, domain.AuthSession{}, err
	}
	if revoked.Valid {
		session.RevokedAt = &revoked.Time
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at = $2 WHERE session_hash = $1`, tokenHash, now.UTC()); err != nil {
		return domain.AuthContext{}, domain.AuthSession{}, err
	}
	session.LastSeenAt = now.UTC()
	auth.Permissions = PermissionsForRole(auth.Membership.Role)
	return auth, session, nil
}

func (s *PostgresStore) RevokeAuthSession(tokenHash string, revokedAt time.Time) error {
	ctx, cancel := storeContext()
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
UPDATE auth_sessions
SET revoked_at = COALESCE(revoked_at, $2)
WHERE session_hash = $1`, tokenHash, revokedAt.UTC())
	return err
}

func (s *PostgresStore) RevokeAuthSessionsByExternalSessionID(provider string, externalSessionID string, revokedAt time.Time) (int64, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if externalSessionID == "" {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_sessions
SET revoked_at = COALESCE(revoked_at, $3)
WHERE external_provider = $1
  AND external_session_id = $2
  AND revoked_at IS NULL`, provider, externalSessionID, revokedAt.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) RevokeAuthSessionsBySubject(provider string, subject string, revokedAt time.Time) (int64, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if subject == "" {
		return 0, nil
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_sessions
SET revoked_at = COALESCE(revoked_at, $3)
WHERE external_provider = $1
  AND external_subject = $2
  AND revoked_at IS NULL`, provider, subject, revokedAt.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *PostgresStore) CleanupAuthSessions(cutoff time.Time, dryRun bool) (domain.AuthSessionRetentionResult, error) {
	ctx, cancel := storeContext()
	defer cancel()

	result := domain.AuthSessionRetentionResult{DryRun: dryRun, Cutoff: cutoff.UTC()}
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM auth_sessions
WHERE expires_at < $1
   OR (revoked_at IS NOT NULL AND revoked_at < $1)`, result.Cutoff).Scan(&result.Scanned); err != nil {
		return result, err
	}
	result.Deleted = result.Scanned
	if dryRun || result.Scanned == 0 {
		return result, nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM auth_sessions
WHERE expires_at < $1
   OR (revoked_at IS NOT NULL AND revoked_at < $1)`, result.Cutoff)
	return result, err
}

func (s *PostgresStore) CreateAuthLoginState(state domain.AuthLoginState) (domain.AuthLoginState, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if state.StateHash == "" {
		return domain.AuthLoginState{}, errors.New("state hash is required")
	}
	if state.NonceHash == "" {
		return domain.AuthLoginState{}, errors.New("nonce hash is required")
	}
	if state.CodeVerifier == "" {
		return domain.AuthLoginState{}, errors.New("code verifier is required")
	}
	if state.ReturnTo == "" {
		state.ReturnTo = "/"
	}
	if state.ExpiresAt.IsZero() {
		state.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO auth_login_states (state_hash, nonce_hash, code_verifier, return_to, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, state_hash, nonce_hash, code_verifier, return_to, created_at, expires_at`,
		state.StateHash, state.NonceHash, state.CodeVerifier, state.ReturnTo, state.ExpiresAt.UTC(),
	).Scan(&state.ID, &state.StateHash, &state.NonceHash, &state.CodeVerifier, &state.ReturnTo, &state.CreatedAt, &state.ExpiresAt)
	return state, err
}

func (s *PostgresStore) ConsumeAuthLoginState(stateHash string, now time.Time) (domain.AuthLoginState, error) {
	ctx, cancel := storeContext()
	defer cancel()

	var state domain.AuthLoginState
	var consumed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
UPDATE auth_login_states
SET consumed_at = $2
WHERE state_hash = $1
  AND consumed_at IS NULL
  AND expires_at > $2
RETURNING id::text, state_hash, nonce_hash, code_verifier, return_to, created_at, expires_at, consumed_at`,
		stateHash, now.UTC(),
	).Scan(&state.ID, &state.StateHash, &state.NonceHash, &state.CodeVerifier, &state.ReturnTo, &state.CreatedAt, &state.ExpiresAt, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.AuthLoginState{}, ErrNotFound
	}
	if consumed.Valid {
		state.ConsumedAt = &consumed.Time
	}
	return state, err
}

func (s *PostgresStore) CleanupAuthLoginStates(cutoff time.Time, dryRun bool) (domain.AuthLoginStateRetentionResult, error) {
	ctx, cancel := storeContext()
	defer cancel()

	result := domain.AuthLoginStateRetentionResult{DryRun: dryRun, Cutoff: cutoff.UTC()}
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM auth_login_states
WHERE expires_at < $1
   OR (consumed_at IS NOT NULL AND consumed_at < $1)`, result.Cutoff).Scan(&result.Scanned); err != nil {
		return result, err
	}
	result.Deleted = result.Scanned
	if dryRun || result.Scanned == 0 {
		return result, nil
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM auth_login_states
WHERE expires_at < $1
   OR (consumed_at IS NOT NULL AND consumed_at < $1)`, result.Cutoff)
	return result, err
}

func (s *PostgresStore) GetRepository(id string) (domain.Repository, error) {
	ctx, cancel := storeContext()
	defer cancel()

	var repo domain.Repository
	var mirror sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT id::text, project_id::text, name, provider, remote_url, default_branch, local_mirror_path, created_at
FROM repositories
WHERE id = $1`, id).Scan(&repo.ID, &repo.ProjectID, &repo.Name, &repo.Provider, &repo.RemoteURL, &repo.DefaultBranch, &mirror, &repo.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Repository{}, ErrNotFound
	}
	repo.LocalMirrorPath = mirror.String
	return repo, err
}

func (s *PostgresStore) ListSkills() []domain.Skill {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT s.id::text, s.org_id::text, s.name, s.description, s.role::text, s.enabled,
       COALESCE(v.version, '') AS version,
       COALESCE(v.content_hash, '') AS content_hash,
       COALESCE(v.path, '') AS path,
       s.created_at
FROM skills s
LEFT JOIN LATERAL (
  SELECT version, content_hash, path
  FROM skill_versions
  WHERE skill_id = s.id
  ORDER BY created_at DESC
  LIMIT 1
) v ON true
ORDER BY s.name ASC`)
	if err != nil {
		s.log.Error("list skills failed", "error", err)
		return nil
	}
	defer rows.Close()

	skills := []domain.Skill{}
	for rows.Next() {
		var skill domain.Skill
		if err := rows.Scan(&skill.ID, &skill.OrgID, &skill.Name, &skill.Description, &skill.Role, &skill.Enabled, &skill.LatestVersion, &skill.ContentHash, &skill.Path, &skill.CreatedAt); err != nil {
			s.log.Error("scan skill failed", "error", err)
			return skills
		}
		skills = append(skills, skill)
	}
	return skills
}

func (s *PostgresStore) ListSkillVersions(skillID string) []domain.SkillVersion {
	if !validUUIDText(skillID) {
		return []domain.SkillVersion{}
	}
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, skill_id::text, version, content_hash, path, created_at
FROM skill_versions
WHERE skill_id = $1::uuid
ORDER BY created_at DESC, version DESC`, skillID)
	if err != nil {
		s.log.Error("list skill versions failed", "skill_id", skillID, "error", err)
		return []domain.SkillVersion{}
	}
	defer rows.Close()

	versions := []domain.SkillVersion{}
	for rows.Next() {
		var version domain.SkillVersion
		if err := rows.Scan(&version.ID, &version.SkillID, &version.Version, &version.ContentHash, &version.Path, &version.CreatedAt); err != nil {
			s.log.Error("scan skill version failed", "error", err)
			return versions
		}
		versions = append(versions, version)
	}
	return versions
}

func (s *PostgresStore) CreateSkill(skill domain.Skill, version domain.SkillVersion) (domain.Skill, error) {
	ctx, cancel := storeContext()
	defer cancel()

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
	if skill.OrgID == "" {
		skill.OrgID = defaultOrgID
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Skill{}, err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `
INSERT INTO skills (org_id, name, description, role, enabled)
VALUES ($1::uuid, $2, $3, $4, $5)
ON CONFLICT (org_id, name) DO UPDATE
SET description = EXCLUDED.description,
    role = EXCLUDED.role,
    enabled = EXCLUDED.enabled
RETURNING id::text, org_id::text, name, description, role::text, enabled, created_at`,
		skill.OrgID, skill.Name, skill.Description, dbRole(skill.Role), skill.Enabled,
	).Scan(&skill.ID, &skill.OrgID, &skill.Name, &skill.Description, &skill.Role, &skill.Enabled, &skill.CreatedAt); err != nil {
		return domain.Skill{}, err
	}

	if err := tx.QueryRowContext(ctx, `
INSERT INTO skill_versions (skill_id, version, content_hash, path)
VALUES ($1::uuid, $2, $3, $4)
ON CONFLICT (skill_id, version) DO UPDATE
SET content_hash = EXCLUDED.content_hash,
    path = EXCLUDED.path
RETURNING id::text, version, content_hash, path, created_at`,
		skill.ID, version.Version, version.ContentHash, version.Path,
	).Scan(&version.ID, &version.Version, &version.ContentHash, &version.Path, &version.CreatedAt); err != nil {
		return domain.Skill{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Skill{}, err
	}
	skill.LatestVersion = version.Version
	skill.ContentHash = version.ContentHash
	skill.Path = version.Path
	return skill, nil
}

func (s *PostgresStore) ListAgentProfiles(projectID string) []domain.AgentProfile {
	if projectID != "" && !validUUIDText(projectID) {
		return []domain.AgentProfile{}
	}
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, COALESCE(project_id::text, ''), name, role::text, model, sandbox_mode,
       approval_policy, executor::text, COALESCE(image, ''), network_enabled, config, created_at
FROM agent_profiles
WHERE ($1 = '' OR project_id = $1::uuid OR project_id IS NULL)
ORDER BY name ASC`, projectID)
	if err != nil {
		s.log.Error("list agent profiles failed", "error", err)
		return nil
	}
	defer rows.Close()

	profiles := []domain.AgentProfile{}
	for rows.Next() {
		profile, err := scanAgentProfile(rows)
		if err != nil {
			s.log.Error("scan agent profile failed", "error", err)
			return profiles
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

func (s *PostgresStore) CreateAgentProfile(profile domain.AgentProfile) (domain.AgentProfile, error) {
	if profile.ProjectID != "" && !validUUIDText(profile.ProjectID) {
		return domain.AgentProfile{}, ErrInvalidID
	}
	ctx, cancel := storeContext()
	defer cancel()

	if profile.Config == nil {
		profile.Config = map[string]any{}
	}
	configBytes, _ := json.Marshal(profile.Config)
	var projectID any
	if profile.ProjectID != "" {
		projectID = profile.ProjectID
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO agent_profiles (project_id, name, role, model, sandbox_mode, approval_policy, executor, image, network_enabled, config)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id::text, COALESCE(project_id::text, ''), name, role::text, model, sandbox_mode,
          approval_policy, executor::text, COALESCE(image, ''), network_enabled, config, created_at`,
		projectID, profile.Name, dbRole(profile.Role), profile.Model, profile.SandboxMode, profile.ApprovalPolicy,
		profile.Executor, profile.Image, profile.NetworkEnabled, configBytes,
	).Scan(&profile.ID, &profile.ProjectID, &profile.Name, &profile.Role, &profile.Model, &profile.SandboxMode,
		&profile.ApprovalPolicy, &profile.Executor, &profile.Image, &profile.NetworkEnabled, &configBytes, &profile.CreatedAt)
	if err != nil {
		return domain.AgentProfile{}, err
	}
	profile.Config = decodeMap(configBytes)
	return profile, nil
}

func (s *PostgresStore) ListExecutorNodes() []domain.ExecutorNode {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, org_id::text, kind::text, name, COALESCE(address, ''), COALESCE(agentd_url, ''),
       COALESCE(host_key_fingerprint, ''), COALESCE(observed_host_key_fingerprint, ''),
       host_key_verified, COALESCE(forced_command, ''), labels, capacity, status,
       last_seen_at, verified_at, created_at
FROM executor_nodes
ORDER BY name ASC`)
	if err != nil {
		s.log.Error("list executor nodes failed", "error", err)
		return nil
	}
	defer rows.Close()

	nodes := []domain.ExecutorNode{}
	for rows.Next() {
		node, err := scanExecutorNode(rows)
		if err != nil {
			s.log.Error("scan executor node failed", "error", err)
			return nodes
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (s *PostgresStore) GetExecutorNode(id string) (domain.ExecutorNode, error) {
	ctx, cancel := storeContext()
	defer cancel()

	node, err := scanExecutorNode(s.db.QueryRowContext(ctx, `
SELECT id::text, org_id::text, kind::text, name, COALESCE(address, ''), COALESCE(agentd_url, ''),
       COALESCE(host_key_fingerprint, ''), COALESCE(observed_host_key_fingerprint, ''),
       host_key_verified, COALESCE(forced_command, ''), labels, capacity, status,
       last_seen_at, verified_at, created_at
FROM executor_nodes
WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExecutorNode{}, ErrNotFound
	}
	return node, err
}

func (s *PostgresStore) RegisterExecutorNode(node domain.ExecutorNode) (domain.ExecutorNode, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if node.Labels == nil {
		node.Labels = map[string]any{}
	}
	if node.Capacity == nil {
		node.Capacity = map[string]any{}
	}
	if node.Status == "" {
		node.Status = "active"
	}
	if node.OrgID == "" {
		node.OrgID = defaultOrgID
	}
	labelsBytes, _ := json.Marshal(node.Labels)
	capacityBytes, _ := json.Marshal(node.Capacity)
	row := s.db.QueryRowContext(ctx, `
INSERT INTO executor_nodes (org_id, kind, name, address, agentd_url, host_key_fingerprint, observed_host_key_fingerprint, host_key_verified, forced_command, labels, capacity, status, last_seen_at, verified_at)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now(), CASE WHEN $8 THEN now() ELSE NULL END)
ON CONFLICT (org_id, name) DO UPDATE
SET kind = EXCLUDED.kind,
    address = EXCLUDED.address,
    agentd_url = EXCLUDED.agentd_url,
    host_key_fingerprint = EXCLUDED.host_key_fingerprint,
    observed_host_key_fingerprint = EXCLUDED.observed_host_key_fingerprint,
    host_key_verified = EXCLUDED.host_key_verified,
    forced_command = EXCLUDED.forced_command,
    labels = EXCLUDED.labels,
    capacity = EXCLUDED.capacity,
    status = EXCLUDED.status,
    last_seen_at = now(),
    verified_at = EXCLUDED.verified_at
RETURNING id::text, org_id::text, kind::text, name, COALESCE(address, ''), COALESCE(agentd_url, ''),
          COALESCE(host_key_fingerprint, ''), COALESCE(observed_host_key_fingerprint, ''),
          host_key_verified, COALESCE(forced_command, ''), labels, capacity, status,
          last_seen_at, verified_at, created_at`,
		node.OrgID, node.Kind, node.Name, node.Address, node.AgentDURL, node.HostKeyFingerprint, node.ObservedHostKeyFingerprint, node.HostKeyVerified, node.ForcedCommand, labelsBytes, capacityBytes, node.Status,
	)
	node, err := scanExecutorNode(row)
	if err != nil {
		return domain.ExecutorNode{}, err
	}
	return node, nil
}

func (s *PostgresStore) VerifyExecutorNodeHostKey(id string, observedFingerprint string) (domain.ExecutorNode, error) {
	ctx, cancel := storeContext()
	defer cancel()

	node, err := scanExecutorNode(s.db.QueryRowContext(ctx, `
UPDATE executor_nodes
SET observed_host_key_fingerprint = $2,
    host_key_fingerprint = CASE WHEN COALESCE(host_key_fingerprint, '') = '' THEN $2 ELSE host_key_fingerprint END,
    host_key_verified = CASE WHEN COALESCE(host_key_fingerprint, '') = '' THEN true ELSE host_key_fingerprint = $2 END,
    status = CASE WHEN (CASE WHEN COALESCE(host_key_fingerprint, '') = '' THEN true ELSE host_key_fingerprint = $2 END) THEN 'active' ELSE 'verification_failed' END,
    verified_at = CASE WHEN (CASE WHEN COALESCE(host_key_fingerprint, '') = '' THEN true ELSE host_key_fingerprint = $2 END) THEN now() ELSE verified_at END,
    last_seen_at = now()
WHERE id = $1
RETURNING id::text, org_id::text, kind::text, name, COALESCE(address, ''), COALESCE(agentd_url, ''),
          COALESCE(host_key_fingerprint, ''), COALESCE(observed_host_key_fingerprint, ''),
          host_key_verified, COALESCE(forced_command, ''), labels, capacity, status,
          last_seen_at, verified_at, created_at`, id, observedFingerprint))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ExecutorNode{}, ErrNotFound
	}
	return node, err
}

func (s *PostgresStore) CreateArtifact(artifact domain.Artifact) (domain.Artifact, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	metadataBytes, _ := json.Marshal(artifact.Metadata)
	err := s.db.QueryRowContext(ctx, `
INSERT INTO artifacts (run_id, kind, name, path, sha256, size_bytes, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id::text, run_id::text, kind, name, path, COALESCE(sha256, ''), COALESCE(size_bytes, 0), metadata, created_at`,
		artifact.RunID, artifact.Kind, artifact.Name, artifact.Path, nullString(artifact.SHA256), nullInt64(artifact.SizeBytes), metadataBytes,
	).Scan(&artifact.ID, &artifact.RunID, &artifact.Kind, &artifact.Name, &artifact.Path, &artifact.SHA256, &artifact.SizeBytes, &metadataBytes, &artifact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Artifact{}, ErrNotFound
	}
	if err != nil {
		return domain.Artifact{}, err
	}
	artifact.Metadata = decodeMap(metadataBytes)
	return artifact, nil
}

func (s *PostgresStore) ListArtifacts(runID string) []domain.Artifact {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, run_id::text, kind, name, path, COALESCE(sha256, ''), COALESCE(size_bytes, 0), metadata, created_at
FROM artifacts
WHERE run_id = $1
ORDER BY created_at ASC`, runID)
	if err != nil {
		s.log.Error("list artifacts failed", "error", err)
		return nil
	}
	defer rows.Close()

	artifacts := []domain.Artifact{}
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			s.log.Error("scan artifact failed", "error", err)
			return artifacts
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts
}

func (s *PostgresStore) GetArtifact(id string) (domain.Artifact, error) {
	ctx, cancel := storeContext()
	defer cancel()

	artifact, err := scanArtifact(s.db.QueryRowContext(ctx, `
SELECT id::text, run_id::text, kind, name, path, COALESCE(sha256, ''), COALESCE(size_bytes, 0), metadata, created_at
FROM artifacts
WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Artifact{}, ErrNotFound
	}
	return artifact, err
}

func (s *PostgresStore) RecordScopeCheck(taskID string, runID string, baseRef string, result domain.ScopeCheckResult) (domain.ScopeCheckRecord, error) {
	ctx, cancel := storeContext()
	defer cancel()

	changedBytes, _ := json.Marshal(result.ChangedFiles)
	violationBytes, _ := json.Marshal(result.Violations)
	var nullableRun any
	if runID != "" {
		nullableRun = runID
	}
	record := domain.ScopeCheckRecord{TaskID: taskID, RunID: runID, BaseRef: baseRef, Result: result}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO scope_checks (task_id, run_id, base_ref, status, changed_files, violations)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id::text, task_id::text, COALESCE(run_id::text, ''), base_ref, status, changed_files, violations, created_at`,
		taskID, nullableRun, baseRef, result.Status, changedBytes, violationBytes,
	).Scan(&record.ID, &record.TaskID, &record.RunID, &record.BaseRef, &record.Result.Status, &changedBytes, &violationBytes, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScopeCheckRecord{}, ErrNotFound
	}
	if err != nil {
		return domain.ScopeCheckRecord{}, err
	}
	record.Result.ChangedFiles = decodeStringSlice(changedBytes)
	record.Result.Violations = decodeStringSlice(violationBytes)
	return record, nil
}

func (s *PostgresStore) LatestScopeCheck(taskID string) (domain.ScopeCheckRecord, error) {
	ctx, cancel := storeContext()
	defer cancel()

	var record domain.ScopeCheckRecord
	var changedBytes []byte
	var violationBytes []byte
	err := s.db.QueryRowContext(ctx, `
SELECT id::text, task_id::text, COALESCE(run_id::text, ''), base_ref, status, changed_files, violations, created_at
FROM scope_checks
WHERE task_id = $1
ORDER BY created_at DESC
LIMIT 1`, taskID).Scan(&record.ID, &record.TaskID, &record.RunID, &record.BaseRef, &record.Result.Status, &changedBytes, &violationBytes, &record.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ScopeCheckRecord{}, ErrNotFound
	}
	if err != nil {
		return domain.ScopeCheckRecord{}, err
	}
	record.Result.ChangedFiles = decodeStringSlice(changedBytes)
	record.Result.Violations = decodeStringSlice(violationBytes)
	return record, nil
}

func (s *PostgresStore) ListApprovals(taskID string) []domain.Approval {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, task_id::text, approval_type, status, reason,
       COALESCE(requested_by::text, ''), COALESCE(approved_by::text, ''), created_at, decided_at
FROM approvals
WHERE ($1 = '' OR task_id = $1::uuid)
ORDER BY created_at DESC`, taskID)
	if err != nil {
		s.log.Error("list approvals failed", "error", err)
		return nil
	}
	defer rows.Close()

	approvals := []domain.Approval{}
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			s.log.Error("scan approval failed", "error", err)
			return approvals
		}
		approvals = append(approvals, approval)
	}
	return approvals
}

func (s *PostgresStore) CreateApproval(approval domain.Approval) (domain.Approval, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if approval.Status == "" {
		approval.Status = "pending"
	}
	if approval.RequestedBy == "" {
		approval.RequestedBy = defaultUserID
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO approvals (task_id, requested_by, approval_type, status, reason)
VALUES ($1, $2::uuid, $3, $4, $5)
RETURNING id::text, task_id::text, approval_type, status, reason,
          COALESCE(requested_by::text, ''), COALESCE(approved_by::text, ''), created_at, decided_at`,
		approval.TaskID, approval.RequestedBy, approval.ApprovalType, approval.Status, approval.Reason,
	)
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Approval{}, ErrNotFound
	}
	return approval, err
}

func (s *PostgresStore) DecideApproval(approvalID string, status string, approvedBy string, reason string) (domain.Approval, error) {
	ctx, cancel := storeContext()
	defer cancel()

	if approvedBy == "" {
		approvedBy = defaultUserID
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE approvals
SET status = $2,
    approved_by = $3::uuid,
    reason = CASE WHEN $4 = '' THEN reason ELSE $4 END,
    decided_at = now()
WHERE id = $1
RETURNING id::text, task_id::text, approval_type, status, reason,
          COALESCE(requested_by::text, ''), COALESCE(approved_by::text, ''), created_at, decided_at`,
		approvalID, status, approvedBy, reason,
	)
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Approval{}, ErrNotFound
	}
	return approval, err
}

func (s *PostgresStore) ListToolCalls() []domain.ToolCall {
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, COALESCE(run_id::text, ''), caller, tool_name, input, output, status, created_at, finished_at
FROM tool_calls
ORDER BY created_at DESC
LIMIT 200`)
	if err != nil {
		s.log.Error("list tool calls failed", "error", err)
		return nil
	}
	defer rows.Close()

	calls := []domain.ToolCall{}
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			s.log.Error("scan tool call failed", "error", err)
			return calls
		}
		calls = append(calls, call)
	}
	return calls
}

func scanAgentProfile(row rowScanner) (domain.AgentProfile, error) {
	var profile domain.AgentProfile
	var configBytes []byte
	err := row.Scan(&profile.ID, &profile.ProjectID, &profile.Name, &profile.Role, &profile.Model, &profile.SandboxMode,
		&profile.ApprovalPolicy, &profile.Executor, &profile.Image, &profile.NetworkEnabled, &configBytes, &profile.CreatedAt)
	if err != nil {
		return domain.AgentProfile{}, err
	}
	profile.Config = decodeMap(configBytes)
	return profile, nil
}

func scanExecutorNode(row rowScanner) (domain.ExecutorNode, error) {
	var node domain.ExecutorNode
	var labelsBytes []byte
	var capacityBytes []byte
	var lastSeen sql.NullTime
	var verifiedAt sql.NullTime
	err := row.Scan(
		&node.ID,
		&node.OrgID,
		&node.Kind,
		&node.Name,
		&node.Address,
		&node.AgentDURL,
		&node.HostKeyFingerprint,
		&node.ObservedHostKeyFingerprint,
		&node.HostKeyVerified,
		&node.ForcedCommand,
		&labelsBytes,
		&capacityBytes,
		&node.Status,
		&lastSeen,
		&verifiedAt,
		&node.CreatedAt,
	)
	if err != nil {
		return domain.ExecutorNode{}, err
	}
	if lastSeen.Valid {
		node.LastSeenAt = &lastSeen.Time
	}
	if verifiedAt.Valid {
		node.VerifiedAt = &verifiedAt.Time
	}
	node.Labels = decodeMap(labelsBytes)
	node.Capacity = decodeMap(capacityBytes)
	return node, nil
}

func scanArtifact(row rowScanner) (domain.Artifact, error) {
	var artifact domain.Artifact
	var metadataBytes []byte
	err := row.Scan(&artifact.ID, &artifact.RunID, &artifact.Kind, &artifact.Name, &artifact.Path, &artifact.SHA256, &artifact.SizeBytes, &metadataBytes, &artifact.CreatedAt)
	if err != nil {
		return domain.Artifact{}, err
	}
	artifact.Metadata = decodeMap(metadataBytes)
	return artifact, nil
}

func scanApproval(row rowScanner) (domain.Approval, error) {
	var approval domain.Approval
	var decidedAt sql.NullTime
	err := row.Scan(&approval.ID, &approval.TaskID, &approval.ApprovalType, &approval.Status, &approval.Reason,
		&approval.RequestedBy, &approval.ApprovedBy, &approval.CreatedAt, &decidedAt)
	if err != nil {
		return domain.Approval{}, err
	}
	if decidedAt.Valid {
		approval.DecidedAt = &decidedAt.Time
	}
	return approval, nil
}

func scanToolCall(row rowScanner) (domain.ToolCall, error) {
	var call domain.ToolCall
	var inputBytes []byte
	var outputBytes []byte
	var finishedAt sql.NullTime
	err := row.Scan(&call.ID, &call.RunID, &call.Caller, &call.ToolName, &inputBytes, &outputBytes, &call.Status, &call.CreatedAt, &finishedAt)
	if err != nil {
		return domain.ToolCall{}, err
	}
	if finishedAt.Valid {
		call.FinishedAt = &finishedAt.Time
	}
	call.Input = decodeMap(inputBytes)
	call.Output = decodeMap(outputBytes)
	return call, nil
}

func decodeStringSlice(data []byte) []string {
	var out []string
	_ = json.Unmarshal(data, &out)
	if out == nil {
		return []string{}
	}
	return out
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func permissionsForRole(role string) []string {
	return PermissionsForRole(role)
}

func PermissionsForRole(role string) []string {
	switch role {
	case "owner", "admin":
		return []string{"*"}
	case "tech_lead":
		return []string{"organizations:read", "projects:read", "repositories:read", "tasks:write", "runs:write", "approvals:write", "nodes:read", "audit:read"}
	case "reviewer":
		return []string{"organizations:read", "projects:read", "tasks:read", "runs:read", "approvals:write", "nodes:read", "audit:read"}
	case "operator":
		return []string{"organizations:read", "projects:read", "tasks:read", "runs:read", "runs:write", "nodes:read", "nodes:write", "audit:read"}
	case "auditor":
		return []string{"organizations:read", "projects:read", "tasks:read", "runs:read", "nodes:read", "audit:read"}
	default:
		return []string{"organizations:read", "projects:read", "tasks:read", "runs:read"}
	}
}
