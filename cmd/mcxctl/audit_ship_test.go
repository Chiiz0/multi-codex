package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestShipAuditSealCopiesVerifiedBundleAndRejectsOverwrite(t *testing.T) {
	input := filepath.Join(t.TempDir(), "seal")
	entries := []domain.AuditLog{{ID: "audit_1", Action: "api.task_create", Payload: map[string]any{"status": "created"}}}
	if _, err := writeAuditSeal(input, entries, store.AuditHashVerification{Valid: true, Total: 1, Hashed: 1}); err != nil {
		t.Fatalf("write seal: %v", err)
	}
	target := filepath.Join(t.TempDir(), "worm")
	receipt, err := shipAuditSeal(input, "file://"+target)
	if err != nil {
		t.Fatalf("ship seal: %v", err)
	}
	destination, _ := receipt["destination"].(string)
	if destination == "" {
		t.Fatalf("receipt = %#v", receipt)
	}
	for _, name := range []string{"audit.jsonl", "manifest.json", "manifest.sha256", "receipt.json"} {
		if _, err := os.Stat(filepath.Join(destination, name)); err != nil {
			t.Fatalf("expected shipped %s: %v", name, err)
		}
	}
	if _, err := shipAuditSeal(input, "file://"+target); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite rejection, got %v", err)
	}
}

func TestShipAuditSealRejectsTamperedBundle(t *testing.T) {
	input := filepath.Join(t.TempDir(), "seal")
	entries := []domain.AuditLog{{ID: "audit_1", Action: "api.task_create", Payload: map[string]any{"status": "created"}}}
	if _, err := writeAuditSeal(input, entries, store.AuditHashVerification{Valid: true, Total: 1, Hashed: 1}); err != nil {
		t.Fatalf("write seal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(input, "audit.jsonl"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatalf("tamper audit file: %v", err)
	}
	_, err := shipAuditSeal(input, filepath.Join(t.TempDir(), "worm"))
	if err == nil || !strings.Contains(err.Error(), "audit hash mismatch") {
		t.Fatalf("expected audit hash mismatch, got %v", err)
	}
}

func TestShipAuditSealPostsMultipartToHTTPCollector(t *testing.T) {
	input := filepath.Join(t.TempDir(), "seal")
	entries := []domain.AuditLog{{ID: "audit_1", Action: "api.task_create", Payload: map[string]any{"status": "created"}}}
	if _, err := writeAuditSeal(input, entries, store.AuditHashVerification{Valid: true, Total: 1, Hashed: 1}); err != nil {
		t.Fatalf("write seal: %v", err)
	}
	var sawAuditFile bool
	var sawManifestSHA bool
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		if r.FormValue("bundle_format") != "multi-codex.audit-seal.v1" || r.FormValue("manifest_sha256") == "" {
			t.Fatalf("form values = %#v", r.Form)
		}
		file, _, err := r.FormFile("audit.jsonl")
		if err != nil {
			t.Fatalf("audit file missing: %v", err)
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		sawAuditFile = strings.Contains(string(data), "api.task_create")
		sawManifestSHA = r.FormValue("manifest_sha256") != ""
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer collector.Close()

	receipt, err := shipAuditSeal(input, collector.URL)
	if err != nil {
		t.Fatalf("ship http seal: %v", err)
	}
	if receipt["remote_status"] != http.StatusOK || !sawAuditFile || !sawManifestSHA {
		t.Fatalf("receipt = %#v, sawAuditFile=%v sawManifestSHA=%v", receipt, sawAuditFile, sawManifestSHA)
	}
}

func TestShipAuditSealPutsBundleToS3ObjectLockTarget(t *testing.T) {
	input := filepath.Join(t.TempDir(), "seal")
	entries := []domain.AuditLog{{ID: "audit_1", Action: "api.task_create", Payload: map[string]any{"status": "created"}}}
	if _, err := writeAuditSeal(input, entries, store.AuditHashVerification{Valid: true, Total: 1, Hashed: 1}); err != nil {
		t.Fatalf("write seal: %v", err)
	}
	uploads := map[string]http.Header{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("If-None-Match") != "*" {
			t.Fatalf("if-none-match = %q", r.Header.Get("If-None-Match"))
		}
		if r.Header.Get("X-Amz-Object-Lock-Mode") != "COMPLIANCE" || r.Header.Get("X-Amz-Object-Lock-Legal-Hold") != "ON" {
			t.Fatalf("object lock headers = %#v", r.Header)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		uploads[r.URL.Path] = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	t.Setenv("MULTICODEX_AUDIT_SHIP_S3_ENDPOINT", collector.URL)
	t.Setenv("MULTICODEX_AUDIT_SHIP_S3_REGION", "us-test-1")
	t.Setenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_MODE", "COMPLIANCE")
	t.Setenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_RETAIN_UNTIL", "2030-01-01T00:00:00Z")
	t.Setenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_LEGAL_HOLD", "ON")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	receipt, err := shipAuditSeal(input, "s3://audit-bucket/compliance")
	if err != nil {
		t.Fatalf("ship s3 seal: %v", err)
	}
	if receipt["immutable_target"] != "s3_object_lock" || receipt["status"] != "shipped" || receipt["receipt_key"] == "" {
		t.Fatalf("receipt = %#v", receipt)
	}
	if len(uploads) != 4 {
		t.Fatalf("uploads = %#v, want 4 bundle objects", uploads)
	}
	var sawReceipt bool
	for path := range uploads {
		if strings.HasSuffix(path, "/receipt.json") {
			sawReceipt = true
		}
	}
	if !sawReceipt {
		t.Fatalf("receipt object not uploaded: %#v", uploads)
	}
}
