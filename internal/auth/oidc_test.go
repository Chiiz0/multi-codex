package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDCVerifierValidatesRS256Token(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := jwksDocument{Keys: []jwkKey{rsaJWK("test-key", &key.PublicKey)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	token := signTestToken(t, key, map[string]any{
		"sub":   "user-123",
		"iss":   "https://issuer.example",
		"aud":   []string{"multi-codex"},
		"email": "dev@example.com",
		"name":  "Dev User",
		"roles": []string{"multi-codex:operator"},
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	})
	verifier := NewOIDCVerifier("https://issuer.example", "multi-codex", server.URL)

	claims, err := verifier.VerifyAuthorization(context.Background(), "Bearer "+token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.Subject != "user-123" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "multi-codex:operator" {
		t.Fatalf("roles = %#v", claims.Roles)
	}
}

func TestOIDCVerifierRejectsAudienceMismatch(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	jwks := jwksDocument{Keys: []jwkKey{rsaJWK("test-key", &key.PublicKey)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	token := signTestToken(t, key, map[string]any{
		"sub": "user-123",
		"iss": "https://issuer.example",
		"aud": "other-audience",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	verifier := NewOIDCVerifier("https://issuer.example", "multi-codex", server.URL)

	if _, err := verifier.VerifyAuthorization(context.Background(), "Bearer "+token); err == nil {
		t.Fatalf("expected audience mismatch")
	}
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"}
	headerBytes, _ := json.Marshal(header)
	claimBytes, _ := json.Marshal(claims)
	unsigned := encodeSegment(headerBytes) + "." + encodeSegment(claimBytes)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return unsigned + "." + encodeSegment(signature)
}

func rsaJWK(kid string, key *rsa.PublicKey) jwkKey {
	return jwkKey{
		KeyType:   "RSA",
		KeyID:     kid,
		Use:       "sig",
		Algorithm: "RS256",
		Modulus:   encodeSegment(key.N.Bytes()),
		Exponent:  encodeSegment(big.NewInt(int64(key.E)).Bytes()),
	}
}

func encodeSegment(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
