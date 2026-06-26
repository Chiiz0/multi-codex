package api

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	authn "github.com/Chiiz0/multi-codex/internal/auth"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestOIDCAuthCodeFlowCreatesSessionAndBackchannelLogoutRevokes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var expectedNonce string
	var expectedCodeChallenge string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{apiTestJWK("test-key", &key.PublicKey)}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			if got := r.Form.Get("client_id"); got != "multi-codex-web" {
				t.Fatalf("client_id = %q", got)
			}
			if got := r.Form.Get("code"); got != "code-ok" {
				t.Fatalf("code = %q", got)
			}
			if got := authn.CodeChallengeS256(r.Form.Get("code_verifier")); got != expectedCodeChallenge {
				t.Fatalf("PKCE challenge = %q, want %q", got, expectedCodeChallenge)
			}
			token := apiSignTestToken(t, key, map[string]any{
				"sub":   "subject-code-flow",
				"sid":   "sid-code-flow",
				"iss":   "https://issuer.example",
				"aud":   []string{"multi-codex"},
				"email": "code-flow@example.com",
				"nonce": expectedNonce,
				"exp":   time.Now().Add(time.Hour).Unix(),
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"id_token": token, "token_type": "Bearer"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()

	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		AuthMode:                 "oidc",
		AuthSessionTTL:           time.Hour,
		AuthLoginStateTTL:        time.Minute,
		OIDCIssuer:               "https://issuer.example",
		OIDCAudience:             "multi-codex",
		OIDCJWKSURL:              idp.URL + "/jwks",
		OIDCClientID:             "multi-codex-web",
		OIDCClientSecret:         "secret",
		OIDCRedirectURL:          "http://multi-codex.example/api/v1/auth/callback",
		OIDCAuthorizationURL:     idp.URL + "/authorize",
		OIDCTokenURL:             idp.URL + "/token",
		OIDCPostLoginRedirectURL: "/#dashboard",
		OIDCDefaultRole:          "viewer",
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	loginReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login?return_to=%2F%23audit", nil)
	loginResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusFound {
		t.Fatalf("login status = %d, body = %s", loginResp.Code, loginResp.Body.String())
	}
	location, err := url.Parse(loginResp.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse auth location: %v", err)
	}
	query := location.Query()
	state := query.Get("state")
	expectedNonce = query.Get("nonce")
	expectedCodeChallenge = query.Get("code_challenge")
	if state == "" || expectedNonce == "" || expectedCodeChallenge == "" {
		t.Fatalf("authorization query missing state/nonce/challenge: %s", location.RawQuery)
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", query.Get("code_challenge_method"))
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=code-ok&state="+url.QueryEscape(state), nil)
	callbackResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body = %s", callbackResp.Code, callbackResp.Body.String())
	}
	if callbackResp.Header().Get("Location") != "/#audit" {
		t.Fatalf("callback redirect = %q", callbackResp.Header().Get("Location"))
	}
	cookies := callbackResp.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authSessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("callback cookies = %#v", cookies)
	}
	sessionCookie := cookies[0]

	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusOK {
		t.Fatalf("cookie auth status = %d, body = %s", meResp.Code, meResp.Body.String())
	}

	logoutToken := apiSignTestToken(t, key, map[string]any{
		"sub": "subject-code-flow",
		"sid": "sid-code-flow",
		"iss": "https://issuer.example",
		"aud": []string{"multi-codex"},
		"jti": "logout-token-1",
		"events": map[string]any{
			"http://schemas.openid.net/event/backchannel-logout": map[string]any{},
		},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/backchannel/logout", strings.NewReader(url.Values{"logout_token": []string{logoutToken}}.Encode()))
	logoutReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logoutResp := httptest.NewRecorder()
	server.Handler().ServeHTTP(logoutResp, logoutReq)
	if logoutResp.Code != http.StatusOK {
		t.Fatalf("backchannel status = %d, body = %s", logoutResp.Code, logoutResp.Body.String())
	}

	meReq = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.AddCookie(sessionCookie)
	meResp = httptest.NewRecorder()
	server.Handler().ServeHTTP(meResp, meReq)
	if meResp.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status = %d, body = %s", meResp.Code, meResp.Body.String())
	}

	var foundStart, foundCallback, foundLogout bool
	for _, entry := range st.ListAuditLogs() {
		switch entry.Action {
		case "api.auth_login_start":
			foundStart = true
		case "api.auth_login_callback":
			foundCallback = true
		case "api.auth_backchannel_logout":
			if entry.Payload["revoked_sessions"] == int64(1) {
				foundLogout = true
			}
		}
	}
	if !foundStart || !foundCallback || !foundLogout {
		t.Fatalf("expected login/backchannel audit rows, start=%v callback=%v logout=%v", foundStart, foundCallback, foundLogout)
	}
}
