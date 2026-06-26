package mcp

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
	"strings"
	"testing"
	"time"

	authn "github.com/Chiiz0/multi-codex/internal/auth"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestOIDCAuthMapsGroupsAndAuditsMCP(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := map[string]any{"keys": []map[string]any{mcpTestJWK("test-key", &key.PublicKey)}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := mcpSignTestToken(t, key, map[string]any{
		"sub":    "subject-1",
		"iss":    "https://issuer.example",
		"aud":    []string{"multi-codex"},
		"groups": []string{"auditors"},
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
			{Claim: "auditors", Value: "auditor"},
		},
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var found bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "mcp.auth_oidc_mapped" && entry.Payload["role"] == "auditor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mcp.auth_oidc_mapped audit row")
	}
}

func TestOIDCAuthRejectsRevokedBearerTokenMCP(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := map[string]any{"keys": []map[string]any{mcpTestJWK("test-key", &key.PublicKey)}}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer jwksServer.Close()

	token := mcpSignTestToken(t, key, map[string]any{
		"sub": "subject-revoked",
		"iss": "https://issuer.example",
		"aud": []string{"multi-codex"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	st := store.NewMemoryStore()
	if _, err := st.RevokeAuthToken(domain.AuthTokenRevocation{TokenHash: authn.TokenHash(token), Subject: "subject-revoked", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{
		AuthMode:        "oidc",
		OIDCIssuer:      "https://issuer.example",
		OIDCAudience:    "multi-codex",
		OIDCJWKSURL:     jwksServer.URL,
		OIDCDefaultRole: "viewer",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "mcp.auth_denied" && entry.Payload["error"] == "bearer token revoked" {
			return
		}
	}
	t.Fatalf("expected mcp.auth_denied audit row")
}

func mcpSignTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"}
	headerBytes, _ := json.Marshal(header)
	claimBytes, _ := json.Marshal(claims)
	unsigned := mcpEncodeSegment(headerBytes) + "." + mcpEncodeSegment(claimBytes)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return unsigned + "." + mcpEncodeSegment(signature)
}

func mcpTestJWK(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"use": "sig",
		"alg": "RS256",
		"n":   mcpEncodeSegment(key.N.Bytes()),
		"e":   mcpEncodeSegment(big.NewInt(int64(key.E)).Bytes()),
	}
}

func mcpEncodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
