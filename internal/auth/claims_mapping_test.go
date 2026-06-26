package auth

import "testing"

func TestMapIdentityUsesExplicitGroupMappings(t *testing.T) {
	mapped := MapIdentity(Claims{
		Subject: "user-123",
		Email:   "dev@example.com",
		Groups:  []string{"platform-admins", "engineering"},
		Roles:   []string{"multi-codex:viewer"},
	}, IdentityMapping{
		DefaultRole:  "viewer",
		DefaultOrgID: "org_default",
		GroupRoleMappings: []ClaimMapping{
			{Claim: "engineering", Value: "operator"},
			{Claim: "platform-admins", Value: "admin"},
		},
		GroupOrgMappings: []ClaimMapping{
			{Claim: "engineering", Value: "org_engineering"},
		},
	})

	if mapped.Role != "operator" {
		t.Fatalf("role = %q", mapped.Role)
	}
	if mapped.OrgID != "org_engineering" {
		t.Fatalf("org id = %q", mapped.OrgID)
	}
	if mapped.DisplayName != "dev@example.com" {
		t.Fatalf("display name = %q", mapped.DisplayName)
	}
	if mapped.MatchedRoleClaim != "engineering" {
		t.Fatalf("matched role claim = %q", mapped.MatchedRoleClaim)
	}
	if mapped.MatchedOrgClaim != "engineering" {
		t.Fatalf("matched org claim = %q", mapped.MatchedOrgClaim)
	}
}

func TestMapIdentityFallsBackToPrefixedRoleThenViewer(t *testing.T) {
	mapped := MapIdentity(Claims{
		Subject: "user-123",
		Groups:  []string{"multi_codex:auditor"},
	}, IdentityMapping{DefaultRole: "not-a-real-role"})

	if mapped.Role != "auditor" {
		t.Fatalf("role = %q", mapped.Role)
	}

	mapped = MapIdentity(Claims{Subject: "user-456"}, IdentityMapping{DefaultRole: "not-a-real-role"})
	if mapped.Role != "viewer" {
		t.Fatalf("fallback role = %q", mapped.Role)
	}
}
