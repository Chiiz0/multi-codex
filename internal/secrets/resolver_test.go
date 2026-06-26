package secrets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvResolverLooksUpHostEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-secret")
	resolver, err := NewResolver("env", "")
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	value, ok, err := resolver.Lookup("OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok || value != "sk-env-secret" {
		t.Fatalf("lookup = %q, %v", value, ok)
	}
}

func TestFileResolverReloadsSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte(`{"OPENAI_API_KEY":"sk-file-one"}`), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	resolver, err := NewResolver("file", path)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	value, ok, err := resolver.Lookup("OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok || value != "sk-file-one" {
		t.Fatalf("lookup = %q, %v", value, ok)
	}
	if err := os.WriteFile(path, []byte(`{"OPENAI_API_KEY":"sk-file-two"}`), 0o600); err != nil {
		t.Fatalf("rotate secret file: %v", err)
	}
	value, ok, err = resolver.Lookup("OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("lookup after rotate: %v", err)
	}
	if !ok || value != "sk-file-two" {
		t.Fatalf("rotated lookup = %q, %v", value, ok)
	}
}

func TestNewResolverRejectsInvalidProvider(t *testing.T) {
	if _, err := NewResolver("unknown", ""); err == nil {
		t.Fatalf("expected unsupported provider error")
	}
	if _, err := NewResolver("file", ""); err == nil {
		t.Fatalf("expected missing file path error")
	}
}

func TestVaultResolverReadsKVv2Secret(t *testing.T) {
	var gotPath string
	var gotToken string
	var gotNamespace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Vault-Token")
		gotNamespace = r.Header.Get("X-Vault-Namespace")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"data":{"OPENAI_API_KEY":"sk-vault-secret","COUNT":3}}}`))
	}))
	defer server.Close()

	resolver, err := NewResolverWithConfig(ResolverConfig{
		Provider:        "vault",
		VaultAddress:    server.URL,
		VaultToken:      "vault-token",
		VaultNamespace:  "engineering",
		VaultMount:      "kv",
		VaultSecretPath: "multi-codex/worker",
	})
	if err != nil {
		t.Fatalf("new vault resolver: %v", err)
	}
	value, ok, err := resolver.Lookup("OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok || value != "sk-vault-secret" {
		t.Fatalf("lookup = %q, %v", value, ok)
	}
	count, ok, err := resolver.Lookup("COUNT")
	if err != nil {
		t.Fatalf("lookup count: %v", err)
	}
	if !ok || count != "3" {
		t.Fatalf("count lookup = %q, %v", count, ok)
	}
	if gotPath != "/v1/kv/data/multi-codex/worker" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotToken != "vault-token" {
		t.Fatalf("vault token header = %q", gotToken)
	}
	if gotNamespace != "engineering" {
		t.Fatalf("vault namespace header = %q", gotNamespace)
	}
}

func TestVaultResolverHandlesMissingSecretAndTokenFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "vault-token")
	if err := os.WriteFile(tokenFile, []byte("token-from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Vault-Token")
		http.NotFound(w, r)
	}))
	defer server.Close()

	resolver, err := NewResolverWithConfig(ResolverConfig{
		Provider:        "vault",
		VaultAddress:    server.URL,
		VaultTokenFile:  tokenFile,
		VaultSecretPath: "multi-codex/worker",
	})
	if err != nil {
		t.Fatalf("new vault resolver: %v", err)
	}
	value, ok, err := resolver.Lookup("OPENAI_API_KEY")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if ok || value != "" {
		t.Fatalf("lookup = %q, %v", value, ok)
	}
	if gotToken != "token-from-file" {
		t.Fatalf("vault token header = %q", gotToken)
	}
}
