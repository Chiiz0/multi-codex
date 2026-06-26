package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OIDCEndpoints struct {
	AuthorizationURL string
	TokenURL         string
}

type AuthCodeConfig struct {
	Issuer           string
	ClientID         string
	ClientSecret     string
	ClientAuthMethod string
	RedirectURL      string
	AuthorizationURL string
	TokenURL         string
}

type TokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func ResolveOIDCEndpoints(ctx context.Context, issuer string, authorizationURL string, tokenURL string) (OIDCEndpoints, error) {
	endpoints := OIDCEndpoints{
		AuthorizationURL: strings.TrimSpace(authorizationURL),
		TokenURL:         strings.TrimSpace(tokenURL),
	}
	if endpoints.AuthorizationURL != "" && endpoints.TokenURL != "" {
		return endpoints, nil
	}
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return endpoints, fmt.Errorf("OIDC issuer or authorization/token URLs are required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return endpoints, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return endpoints, fmt.Errorf("discover issuer: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return endpoints, fmt.Errorf("discovery status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return endpoints, err
	}
	var document discoveryDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return endpoints, fmt.Errorf("parse discovery: %v", err)
	}
	if endpoints.AuthorizationURL == "" {
		endpoints.AuthorizationURL = document.AuthorizationEndpoint
	}
	if endpoints.TokenURL == "" {
		endpoints.TokenURL = document.TokenEndpoint
	}
	if endpoints.AuthorizationURL == "" || endpoints.TokenURL == "" {
		return endpoints, fmt.Errorf("discovery missing authorization_endpoint or token_endpoint")
	}
	return endpoints, nil
}

func BuildAuthorizationURL(baseURL string, clientID string, redirectURL string, state string, nonce string, codeVerifier string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("response_type", "code")
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("scope", "openid email profile")
	values.Set("state", state)
	values.Set("nonce", nonce)
	values.Set("code_challenge", CodeChallengeS256(codeVerifier))
	values.Set("code_challenge_method", "S256")
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func ExchangeAuthCode(ctx context.Context, cfg AuthCodeConfig, code string, codeVerifier string) (TokenResponse, error) {
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if tokenURL == "" {
		endpoints, err := ResolveOIDCEndpoints(ctx, cfg.Issuer, cfg.AuthorizationURL, cfg.TokenURL)
		if err != nil {
			return TokenResponse{}, err
		}
		tokenURL = endpoints.TokenURL
	}
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", cfg.RedirectURL)
	values.Set("code_verifier", codeVerifier)
	clientAuthMethod := strings.TrimSpace(cfg.ClientAuthMethod)
	switch clientAuthMethod {
	case "", "client_secret_post", "client_secret_basic", "none":
	default:
		clientAuthMethod = "client_secret_post"
	}
	if clientAuthMethod != "client_secret_basic" || cfg.ClientSecret == "" {
		values.Set("client_id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" && clientAuthMethod == "client_secret_post" {
		values.Set("client_secret", cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if cfg.ClientSecret != "" && clientAuthMethod == "client_secret_basic" {
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("exchange code: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return TokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return TokenResponse{}, fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return TokenResponse{}, fmt.Errorf("parse token response: %v", err)
	}
	if token.IDToken == "" {
		return TokenResponse{}, fmt.Errorf("token response missing id_token")
	}
	return token, nil
}

func CodeChallengeS256(codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
