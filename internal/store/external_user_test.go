package store

import "testing"

func TestMemoryExternalUserUpsertIsStable(t *testing.T) {
	st := NewMemoryStore()

	first, err := st.UpsertExternalUser("oidc", "subject-1", "dev@example.com", "Dev User", "viewer", "")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := st.UpsertExternalUser("oidc", "subject-1", "dev@example.com", "Dev User", "operator", "org_custom")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if first.User.ID != second.User.ID {
		t.Fatalf("user id changed: %q -> %q", first.User.ID, second.User.ID)
	}
	if second.Membership.Role != "operator" {
		t.Fatalf("role = %q", second.Membership.Role)
	}
	if second.Membership.OrgID != "org_custom" {
		t.Fatalf("org id = %q", second.Membership.OrgID)
	}
	if len(second.Permissions) == 0 {
		t.Fatalf("expected permissions for updated role")
	}
}
