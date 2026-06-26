package auth

import "strings"

type ClaimMapping struct {
	Claim string
	Value string
}

type IdentityMapping struct {
	DefaultRole       string
	DefaultOrgID      string
	GroupRoleMappings []ClaimMapping
	GroupOrgMappings  []ClaimMapping
}

type MappedIdentity struct {
	Role             string
	OrgID            string
	DisplayName      string
	MatchedRoleClaim string
	MatchedOrgClaim  string
}

func MapIdentity(claims Claims, mapping IdentityMapping) MappedIdentity {
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.Email
	}
	if displayName == "" {
		displayName = claims.Subject
	}

	role, roleClaim := mappedRole(claims, mapping)
	orgID, orgClaim := mappedOrg(claims, mapping)
	return MappedIdentity{
		Role:             role,
		OrgID:            orgID,
		DisplayName:      displayName,
		MatchedRoleClaim: roleClaim,
		MatchedOrgClaim:  orgClaim,
	}
}

func mappedRole(claims Claims, mapping IdentityMapping) (string, string) {
	for _, candidate := range mapping.GroupRoleMappings {
		if !claimListContains(claims.Groups, candidate.Claim) {
			continue
		}
		if role := normalizeRole(candidate.Value); role != "" {
			return role, candidate.Claim
		}
	}
	for _, value := range append(claims.Roles, claims.Groups...) {
		normalized := strings.ToLower(strings.TrimSpace(value))
		normalized = strings.TrimPrefix(normalized, "multi-codex:")
		normalized = strings.TrimPrefix(normalized, "multi_codex:")
		if role := normalizeRole(normalized); role != "" {
			return role, value
		}
	}
	if role := normalizeRole(mapping.DefaultRole); role != "" {
		return role, ""
	}
	return "viewer", ""
}

func mappedOrg(claims Claims, mapping IdentityMapping) (string, string) {
	for _, candidate := range mapping.GroupOrgMappings {
		if claimListContains(claims.Groups, candidate.Claim) {
			return strings.TrimSpace(candidate.Value), candidate.Claim
		}
	}
	return strings.TrimSpace(mapping.DefaultOrgID), ""
}

func claimListContains(values []string, expected string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), expected) {
			return true
		}
	}
	return false
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "owner", "admin", "tech_lead", "reviewer", "operator", "auditor", "viewer":
		return strings.ToLower(strings.TrimSpace(role))
	default:
		return ""
	}
}
