package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authn "github.com/Chiiz0/multi-codex/internal/auth"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestOIDCAuthMapsGroupsToRoleAndOrg(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := map[string]any{"keys": []map[string]any{apiTestJWK("test-key", &key.PublicKey)}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := apiSignTestToken(t, key, map[string]any{
		"sub":    "subject-1",
		"iss":    "https://issuer.example",
		"aud":    []string{"multi-codex"},
		"email":  "dev@example.com",
		"groups": []string{"engineering"},
		"exp":    time.Now().Add(time.Hour).Unix(),
	})
	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:        "oidc",
		OIDCIssuer:      "https://issuer.example",
		OIDCAudience:    "multi-codex",
		OIDCJWKSURL:     jwksServer.URL,
		OIDCDefaultRole: "viewer",
		OIDCGroupRoleMap: []authn.ClaimMapping{
			{Claim: "engineering", Value: "operator"},
		},
		OIDCGroupOrgMap: []authn.ClaimMapping{
			{Claim: "engineering", Value: "org_engineering"},
		},
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var auth domain.AuthContext
	if err := json.Unmarshal(resp.Body.Bytes(), &auth); err != nil {
		t.Fatalf("decode auth: %v", err)
	}
	if auth.Membership.Role != "operator" {
		t.Fatalf("role = %q", auth.Membership.Role)
	}
	if auth.Membership.OrgID != "org_engineering" {
		t.Fatalf("org id = %q", auth.Membership.OrgID)
	}

	var found bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.auth_oidc_mapped" && entry.ActorID == auth.User.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected api.auth_oidc_mapped audit row")
	}
}

func TestAuthCapabilitiesIsPublicInOIDCMode(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:        "oidc",
		OIDCIssuer:      "https://issuer.example",
		OIDCAudience:    "multi-codex",
		OIDCJWKSURL:     "http://127.0.0.1:1/jwks",
		OIDCClientID:    "multi-codex-web",
		OIDCRedirectURL: "http://127.0.0.1:5173/api/v1/auth/callback",
		OIDCDefaultRole: "viewer",
		AuthSessionTTL:  2 * time.Hour,
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/capabilities", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		AuthMode          string `json:"auth_mode"`
		OIDCConfigured    bool   `json:"oidc_configured"`
		SessionTTLSeconds int64  `json:"session_ttl_seconds"`
		DefaultRole       string `json:"default_role"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if payload.AuthMode != "oidc" || !payload.OIDCConfigured || payload.SessionTTLSeconds != int64((2*time.Hour).Seconds()) || payload.DefaultRole != "viewer" {
		t.Fatalf("unexpected capabilities: %+v", payload)
	}
}

func apiSignTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"}
	headerBytes, _ := json.Marshal(header)
	claimBytes, _ := json.Marshal(claims)
	unsigned := apiEncodeSegment(headerBytes) + "." + apiEncodeSegment(claimBytes)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return unsigned + "." + apiEncodeSegment(signature)
}

func apiTestJWK(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   apiEncodeSegment(key.N.Bytes()),
		"e":   apiEncodeSegment(big.NewInt(int64(key.E)).Bytes()),
	}
}

func apiEncodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
