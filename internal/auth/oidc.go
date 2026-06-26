package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrMissingBearer = errors.New("missing bearer token")
	ErrInvalidToken  = errors.New("invalid OIDC token")
)

type Claims struct {
	Subject   string
	Issuer    string
	Audience  []string
	Email     string
	Name      string
	Roles     []string
	Groups    []string
	Nonce     string
	SessionID string
	IssuedAt  time.Time
	TokenID   string
	Events    []string
	ExpiresAt time.Time
}

type OIDCVerifier struct {
	issuer      string
	audience    string
	jwksURL     string
	httpClient  *http.Client
	mu          sync.Mutex
	cachedKeys  []jwkKey
	keysFetched time.Time
}

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type tokenClaims struct {
	Subject        string                     `json:"sub"`
	Issuer         string                     `json:"iss"`
	Audience       json.RawMessage            `json:"aud"`
	Email          string                     `json:"email"`
	Name           string                     `json:"name"`
	ExpiresAt      int64                      `json:"exp"`
	NotBefore      int64                      `json:"nbf"`
	IssuedAt       int64                      `json:"iat"`
	TokenID        string                     `json:"jti"`
	Nonce          string                     `json:"nonce"`
	SessionID      string                     `json:"sid"`
	Groups         []string                   `json:"groups"`
	Roles          []string                   `json:"roles"`
	RealmAccess    tokenRoleSet               `json:"realm_access"`
	ResourceAccess map[string]tokenRoleSet    `json:"resource_access"`
	Events         map[string]json.RawMessage `json:"events"`
	Custom         map[string]json.RawMessage `json:"-"`
}

type tokenRoleSet struct {
	Roles []string `json:"roles"`
}

type discoveryDocument struct {
	JWKSURI               string `json:"jwks_uri"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func NewOIDCVerifier(issuer string, audience string, jwksURL string) *OIDCVerifier {
	return &OIDCVerifier{
		issuer:     strings.TrimRight(issuer, "/"),
		audience:   audience,
		jwksURL:    jwksURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (v *OIDCVerifier) VerifyAuthorization(ctx context.Context, header string) (Claims, error) {
	token, ok := BearerToken(header)
	if !ok {
		return Claims{}, ErrMissingBearer
	}
	return v.VerifyToken(ctx, token)
}

func (v *OIDCVerifier) VerifyToken(ctx context.Context, token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, fmt.Errorf("%w: expected JWT with three parts", ErrInvalidToken)
	}
	headerBytes, err := decodeSegment(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: decode header: %v", ErrInvalidToken, err)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return Claims{}, fmt.Errorf("%w: parse header: %v", ErrInvalidToken, err)
	}
	if header.Algorithm != "RS256" {
		return Claims{}, fmt.Errorf("%w: unsupported alg %q", ErrInvalidToken, header.Algorithm)
	}

	payloadBytes, err := decodeSegment(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: decode payload: %v", ErrInvalidToken, err)
	}
	var raw tokenClaims
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return Claims{}, fmt.Errorf("%w: parse claims: %v", ErrInvalidToken, err)
	}
	claims, err := v.validateClaims(raw)
	if err != nil {
		return Claims{}, err
	}

	signature, err := decodeSegment(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: decode signature: %v", ErrInvalidToken, err)
	}
	key, err := v.publicKey(ctx, header.KeyID)
	if err != nil {
		return Claims{}, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return Claims{}, fmt.Errorf("%w: signature verification failed", ErrInvalidToken)
	}
	return claims, nil
}

func (v *OIDCVerifier) validateClaims(raw tokenClaims) (Claims, error) {
	if raw.Subject == "" {
		return Claims{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	}
	if v.issuer != "" && raw.Issuer != v.issuer {
		return Claims{}, fmt.Errorf("%w: issuer mismatch", ErrInvalidToken)
	}
	now := time.Now().Unix()
	if raw.ExpiresAt == 0 || raw.ExpiresAt < now-60 {
		return Claims{}, fmt.Errorf("%w: token expired", ErrInvalidToken)
	}
	if raw.NotBefore != 0 && raw.NotBefore > now+60 {
		return Claims{}, fmt.Errorf("%w: token not yet valid", ErrInvalidToken)
	}
	audience, err := parseAudience(raw.Audience)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: invalid audience", ErrInvalidToken)
	}
	if v.audience != "" && !contains(audience, v.audience) {
		return Claims{}, fmt.Errorf("%w: audience mismatch", ErrInvalidToken)
	}
	roles := append([]string{}, raw.Roles...)
	roles = append(roles, raw.RealmAccess.Roles...)
	for _, access := range raw.ResourceAccess {
		roles = append(roles, access.Roles...)
	}
	return Claims{
		Subject:   raw.Subject,
		Issuer:    raw.Issuer,
		Audience:  audience,
		Email:     raw.Email,
		Name:      raw.Name,
		Roles:     dedupe(roles),
		Groups:    dedupe(raw.Groups),
		Nonce:     raw.Nonce,
		SessionID: raw.SessionID,
		IssuedAt:  time.Unix(raw.IssuedAt, 0).UTC(),
		TokenID:   raw.TokenID,
		Events:    eventTypes(raw.Events),
		ExpiresAt: time.Unix(raw.ExpiresAt, 0).UTC(),
	}, nil
}

func BearerToken(header string) (string, bool) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return token, token != ""
}

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (v *OIDCVerifier) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	keys, err := v.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if kid != "" && key.KeyID != kid {
			continue
		}
		if key.KeyType != "RSA" {
			continue
		}
		return key.rsaPublicKey()
	}
	return nil, fmt.Errorf("%w: no matching JWKS key", ErrInvalidToken)
}

func (v *OIDCVerifier) keys(ctx context.Context) ([]jwkKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if len(v.cachedKeys) > 0 && time.Since(v.keysFetched) < 10*time.Minute {
		return v.cachedKeys, nil
	}
	jwksURL := v.jwksURL
	if jwksURL == "" {
		discovered, err := v.discoverJWKSURL(ctx)
		if err != nil {
			return nil, err
		}
		jwksURL = discovered
		v.jwksURL = discovered
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch JWKS: %v", ErrInvalidToken, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: JWKS status %d", ErrInvalidToken, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, fmt.Errorf("%w: parse JWKS: %v", ErrInvalidToken, err)
	}
	if len(document.Keys) == 0 {
		return nil, fmt.Errorf("%w: JWKS has no keys", ErrInvalidToken)
	}
	v.cachedKeys = document.Keys
	v.keysFetched = time.Now().UTC()
	return v.cachedKeys, nil
}

func (v *OIDCVerifier) discoverJWKSURL(ctx context.Context) (string, error) {
	if v.issuer == "" {
		return "", fmt.Errorf("%w: OIDC issuer or JWKS URL is required", ErrInvalidToken)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: discover issuer: %v", ErrInvalidToken, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: discovery status %d", ErrInvalidToken, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return "", err
	}
	var document discoveryDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return "", fmt.Errorf("%w: parse discovery: %v", ErrInvalidToken, err)
	}
	if document.JWKSURI == "" {
		return "", fmt.Errorf("%w: discovery missing jwks_uri", ErrInvalidToken)
	}
	return document.JWKSURI, nil
}

func (key jwkKey) rsaPublicKey() (*rsa.PublicKey, error) {
	nBytes, err := decodeSegment(key.Modulus)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid RSA modulus", ErrInvalidToken)
	}
	eBytes, err := decodeSegment(key.Exponent)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid RSA exponent", ErrInvalidToken)
	}
	exponent := big.NewInt(0).SetBytes(eBytes).Int64()
	if exponent == 0 {
		return nil, fmt.Errorf("%w: invalid RSA exponent", ErrInvalidToken)
	}
	return &rsa.PublicKey{N: big.NewInt(0).SetBytes(nBytes), E: int(exponent)}, nil
}

func parseAudience(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err != nil {
		return nil, err
	}
	return multi, nil
}

func decodeSegment(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func eventTypes(events map[string]json.RawMessage) []string {
	out := make([]string, 0, len(events))
	for eventType := range events {
		if eventType == "" {
			continue
		}
		out = append(out, eventType)
	}
	return out
}
