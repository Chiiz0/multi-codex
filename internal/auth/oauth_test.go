package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchangeAuthCodeSupportsClientSecretBasic(t *testing.T) {
	var sawBasic bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if ok && user == "multi-codex-web" && password == "secret" {
			sawBasic = true
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("client_secret"); got != "" {
			t.Fatalf("client_secret form value = %q, want empty for basic auth", got)
		}
		if got := r.Form.Get("client_id"); got != "" {
			t.Fatalf("client_id form value = %q, want empty for confidential basic auth", got)
		}
		_ = json.NewEncoder(w).Encode(TokenResponse{IDToken: "header.payload.signature", TokenType: "Bearer"})
	}))
	defer server.Close()

	token, err := ExchangeAuthCode(context.Background(), AuthCodeConfig{
		ClientID:         "multi-codex-web",
		ClientSecret:     "secret",
		ClientAuthMethod: "client_secret_basic",
		RedirectURL:      "https://multi-codex.example/api/v1/auth/callback",
		TokenURL:         server.URL,
	}, "code-ok", "verifier")
	if err != nil {
		t.Fatalf("exchange code: %v", err)
	}
	if token.IDToken == "" {
		t.Fatalf("id token should be present")
	}
	if !sawBasic {
		t.Fatalf("expected HTTP basic client authentication")
	}
}
