package api

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestAuthLogoutAuditsDecision(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{AuthMode: "local"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cookie := localSessionCookie(t, server.Handler())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(cookie)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", resp.Code, resp.Body.String())
	}
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.auth_logout" && entry.ActorID == "user_local_dev" {
			return
		}
	}
	t.Fatalf("expected api.auth_logout audit row")
}

func TestLocalAuthRequiresBrowserSession(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{AuthMode: "local"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous local auth status = %d, body = %s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", bytes.NewReader([]byte(`{"email":"local-dev@multi-codex.invalid","password":"wrong-password"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("bad password session status = %d, body = %s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", bytes.NewReader([]byte(`{"email":"local-dev@multi-codex.invalid","password":"admin123"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", resp.Code, resp.Body.String())
	}
	cookies := resp.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authSessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("session cookies = %#v", cookies)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(cookies[0])
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("session local auth status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestOIDCAuthLogoutRevokesBearerToken(t *testing.T) {
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
		"sub":   "subject-logout",
		"iss":   "https://issuer.example",
		"aud":   []string{"multi-codex"},
		"email": "logout@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:        "oidc",
		OIDCIssuer:      "https://issuer.example",
		OIDCAudience:    "multi-codex",
		OIDCJWKSURL:     jwksServer.URL,
		OIDCDefaultRole: "viewer",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var foundLogout, foundDenied bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.auth_logout" && entry.Payload["token_revoked"] == true {
			foundLogout = true
		}
		if entry.Action == "api.auth_denied" && entry.Payload["error"] == "bearer token revoked" {
			foundDenied = true
		}
	}
	if !foundLogout || !foundDenied {
		t.Fatalf("expected logout and denied audit rows, logout=%v denied=%v", foundLogout, foundDenied)
	}
}

func TestOIDCBrowserSessionCookieAuthenticatesAndLogoutRevokes(t *testing.T) {
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
		"sub":   "subject-session",
		"iss":   "https://issuer.example",
		"aud":   []string{"multi-codex"},
		"email": "session@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:        "oidc",
		AuthSessionTTL:  time.Hour,
		OIDCIssuer:      "https://issuer.example",
		OIDCAudience:    "multi-codex",
		OIDCJWKSURL:     jwksServer.URL,
		OIDCDefaultRole: "viewer",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("session status = %d, body = %s", resp.Code, resp.Body.String())
	}
	cookies := resp.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authSessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("session cookies = %#v", cookies)
	}
	sessionCookie := cookies[0]

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(sessionCookie)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("cookie auth status = %d, body = %s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(sessionCookie)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("logout status = %d, body = %s", resp.Code, resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(sessionCookie)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var foundCreate, foundLogout bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "api.auth_session_create" {
			foundCreate = true
		}
		if entry.Action == "api.auth_logout" && entry.Payload["session_revoked"] == true {
			foundLogout = true
		}
	}
	if !foundCreate || !foundLogout {
		t.Fatalf("expected session create/logout audit rows, create=%v logout=%v", foundCreate, foundLogout)
	}
}
