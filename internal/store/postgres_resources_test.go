package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestPostgresListAgentProfilesRejectsInvalidProjectIDBeforeQuery(t *testing.T) {
	st := &PostgresStore{}

	profiles := st.ListAgentProfiles("proj_demo")
	if len(profiles) != 0 {
		t.Fatalf("profiles length = %d, want 0", len(profiles))
	}
	encoded, err := json.Marshal(profiles)
	if err != nil {
		t.Fatalf("marshal profiles: %v", err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded profiles = %s, want []", encoded)
	}
}

func TestPostgresCreateAgentProfileRejectsInvalidProjectIDBeforeQuery(t *testing.T) {
	st := &PostgresStore{}

	_, err := st.CreateAgentProfile(domain.AgentProfile{ProjectID: "proj_demo", Name: "feature"})
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("CreateAgentProfile error = %v, want ErrInvalidID", err)
	}
}
