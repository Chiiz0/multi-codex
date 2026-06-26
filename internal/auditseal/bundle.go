package auditseal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func Write(output string, entries []domain.AuditLog, verification store.AuditHashVerification) (map[string]any, error) {
	if info, err := os.Stat(output); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("output exists and is not a directory: %s", output)
		}
		children, err := os.ReadDir(output)
		if err != nil {
			return nil, err
		}
		if len(children) > 0 {
			return nil, fmt.Errorf("output directory must be empty: %s", output)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return nil, err
	}
	auditPath := filepath.Join(output, "audit.jsonl")
	auditFile, err := os.OpenFile(auditPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	auditHash := sha256.New()
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			_ = auditFile.Close()
			return nil, err
		}
		line = append(line, '\n')
		if _, err := auditFile.Write(line); err != nil {
			_ = auditFile.Close()
			return nil, err
		}
		_, _ = auditHash.Write(line)
	}
	if err := auditFile.Close(); err != nil {
		return nil, err
	}
	manifest := map[string]any{
		"created_at":       time.Now().UTC(),
		"audit_file":       "audit.jsonl",
		"audit_sha256":     hex.EncodeToString(auditHash.Sum(nil)),
		"entry_count":      len(entries),
		"verification":     verification,
		"bundle_format":    "multi-codex.audit-seal.v1",
		"immutable_target": "external_worm_or_siem",
	}
	if len(entries) > 0 {
		manifest["first_audit_id"] = entries[0].ID
		manifest["last_audit_id"] = entries[len(entries)-1].ID
	}
	manifestPath := filepath.Join(output, "manifest.json")
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		return nil, err
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	manifestSum := sha256.Sum256(manifestBytes)
	manifestHash := hex.EncodeToString(manifestSum[:])
	if err := os.WriteFile(filepath.Join(output, "manifest.sha256"), []byte(manifestHash+"  manifest.json\n"), 0o644); err != nil {
		return nil, err
	}
	manifest["manifest_sha256"] = manifestHash
	manifest["output"] = output
	return manifest, nil
}

func Ship(input string, target string) (map[string]any, error) {
	manifest, manifestSHA, err := VerifyBundle(input)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return shipHTTP(input, target, manifest, manifestSHA)
	}
	if strings.HasPrefix(target, "s3://") {
		return shipS3(input, target, manifest, manifestSHA)
	}
	targetRoot := strings.TrimPrefix(target, "file://")
	if targetRoot == "" {
		return nil, fmt.Errorf("audit ship target is required")
	}
	bundleName := filepath.Base(filepath.Clean(input))
	if bundleName == "." || bundleName == string(os.PathSeparator) {
		bundleName = "audit-seal-" + manifestSHA[:12]
	}
	destination := filepath.Join(targetRoot, bundleName)
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(destination, 0o755); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("audit ship destination already exists: %s", destination)
		}
		return nil, err
	}
	for _, name := range []string{"audit.jsonl", "manifest.json", "manifest.sha256"} {
		if err := copyRegularFileExclusive(filepath.Join(input, name), filepath.Join(destination, name)); err != nil {
			return nil, err
		}
	}
	receipt := map[string]any{
		"shipped_at":       time.Now().UTC(),
		"input":            input,
		"target":           target,
		"destination":      destination,
		"bundle_format":    manifest["bundle_format"],
		"entry_count":      manifest["entry_count"],
		"audit_sha256":     manifest["audit_sha256"],
		"manifest_sha256":  manifestSHA,
		"immutable_target": "external_worm_or_siem",
		"status":           "shipped",
	}
	if err := writeJSONFileExclusive(filepath.Join(destination, "receipt.json"), receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func VerifyBundle(input string) (map[string]any, string, error) {
	manifestPath := filepath.Join(input, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, "", err
	}
	manifestHash := sha256.Sum256(manifestBytes)
	manifestSHA := hex.EncodeToString(manifestHash[:])
	expectedManifestSHA, err := readManifestSHA(filepath.Join(input, "manifest.sha256"))
	if err != nil {
		return nil, "", err
	}
	if manifestSHA != expectedManifestSHA {
		return nil, "", fmt.Errorf("manifest hash mismatch: got %s, want %s", manifestSHA, expectedManifestSHA)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, "", err
	}
	if manifest["bundle_format"] != "multi-codex.audit-seal.v1" {
		return nil, "", fmt.Errorf("unsupported audit seal bundle format: %v", manifest["bundle_format"])
	}
	auditBytes, err := os.ReadFile(filepath.Join(input, "audit.jsonl"))
	if err != nil {
		return nil, "", err
	}
	auditHash := sha256.Sum256(auditBytes)
	auditSHA := hex.EncodeToString(auditHash[:])
	if manifest["audit_sha256"] != auditSHA {
		return nil, "", fmt.Errorf("audit hash mismatch: got %s, want %v", auditSHA, manifest["audit_sha256"])
	}
	return manifest, manifestSHA, nil
}

func shipHTTP(input string, target string, manifest map[string]any, manifestSHA string) (map[string]any, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range []string{"audit.jsonl", "manifest.json", "manifest.sha256"} {
		if err := addMultipartFile(writer, name, filepath.Join(input, name)); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	fields := map[string]string{
		"bundle_format":    fmt.Sprint(manifest["bundle_format"]),
		"entry_count":      fmt.Sprint(manifest["entry_count"]),
		"audit_sha256":     fmt.Sprint(manifest["audit_sha256"]),
		"manifest_sha256":  manifestSHA,
		"immutable_target": "external_worm_or_siem",
	}
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			_ = writer.Close()
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, target, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("audit ship target returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return map[string]any{
		"shipped_at":       time.Now().UTC(),
		"target":           target,
		"bundle_format":    manifest["bundle_format"],
		"entry_count":      manifest["entry_count"],
		"audit_sha256":     manifest["audit_sha256"],
		"manifest_sha256":  manifestSHA,
		"immutable_target": "external_worm_or_siem",
		"status":           "shipped",
		"remote_status":    resp.StatusCode,
	}, nil
}

type s3ShipConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	ObjectLockMode  string
	RetainUntil     string
	LegalHold       string
}

func shipS3(input string, target string, manifest map[string]any, manifestSHA string) (map[string]any, error) {
	bucket, prefix, err := parseS3Target(target)
	if err != nil {
		return nil, err
	}
	cfg := s3ShipConfigFromEnv()
	if cfg.Region == "" {
		return nil, fmt.Errorf("MULTICODEX_AUDIT_SHIP_S3_REGION or AWS_REGION is required for s3 audit ship")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are required for s3 audit ship")
	}
	bundleName := filepath.Base(filepath.Clean(input))
	if bundleName == "." || bundleName == string(os.PathSeparator) {
		bundleName = "audit-seal-" + manifestSHA[:12]
	}
	keyPrefix := joinS3Key(prefix, bundleName)
	objectKeys := []string{}
	for _, name := range []string{"audit.jsonl", "manifest.json", "manifest.sha256"} {
		body, err := os.ReadFile(filepath.Join(input, name))
		if err != nil {
			return nil, err
		}
		key := joinS3Key(keyPrefix, name)
		if err := putS3Object(bucket, key, contentTypeForBundleFile(name), body, cfg); err != nil {
			return nil, err
		}
		objectKeys = append(objectKeys, key)
	}
	receipt := map[string]any{
		"shipped_at":       time.Now().UTC(),
		"input":            input,
		"target":           target,
		"bucket":           bucket,
		"key_prefix":       keyPrefix,
		"object_keys":      objectKeys,
		"bundle_format":    manifest["bundle_format"],
		"entry_count":      manifest["entry_count"],
		"audit_sha256":     manifest["audit_sha256"],
		"manifest_sha256":  manifestSHA,
		"immutable_target": "s3_object_lock",
		"status":           "shipped",
	}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	receiptKey := joinS3Key(keyPrefix, "receipt.json")
	if err := putS3Object(bucket, receiptKey, "application/json", append(receiptBytes, '\n'), cfg); err != nil {
		return nil, err
	}
	receipt["receipt_key"] = receiptKey
	return receipt, nil
}

func parseS3Target(target string) (string, string, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme != "s3" || parsed.Host == "" {
		return "", "", fmt.Errorf("s3 audit ship target must be s3://bucket/prefix")
	}
	return parsed.Host, strings.Trim(parsed.Path, "/"), nil
}

func s3ShipConfigFromEnv() s3ShipConfig {
	region := os.Getenv("MULTICODEX_AUDIT_SHIP_S3_REGION")
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	return s3ShipConfig{
		Region:          region,
		Endpoint:        strings.TrimRight(os.Getenv("MULTICODEX_AUDIT_SHIP_S3_ENDPOINT"), "/"),
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		ObjectLockMode:  os.Getenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_MODE"),
		RetainUntil:     os.Getenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_RETAIN_UNTIL"),
		LegalHold:       os.Getenv("MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_LEGAL_HOLD"),
	}
}

func putS3Object(bucket string, key string, contentType string, body []byte, cfg s3ShipConfig) error {
	objectURL, err := s3ObjectURL(bucket, key, cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, objectURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	payloadHash := sha256.Sum256(body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(payloadHash[:]))
	req.Header.Set("X-Amz-Date", time.Now().UTC().Format("20060102T150405Z"))
	if cfg.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", cfg.SessionToken)
	}
	if cfg.ObjectLockMode != "" {
		req.Header.Set("X-Amz-Object-Lock-Mode", cfg.ObjectLockMode)
	}
	if cfg.RetainUntil != "" {
		req.Header.Set("X-Amz-Object-Lock-Retain-Until-Date", cfg.RetainUntil)
	}
	if cfg.LegalHold != "" {
		req.Header.Set("X-Amz-Object-Lock-Legal-Hold", cfg.LegalHold)
	}
	signS3Request(req, cfg, req.Header.Get("X-Amz-Content-Sha256"))
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("s3 audit ship PUT %s returned %d: %s", key, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func s3ObjectURL(bucket string, key string, cfg s3ShipConfig) (string, error) {
	escapedKey := escapeS3Key(key)
	if cfg.Endpoint != "" {
		return cfg.Endpoint + "/" + url.PathEscape(bucket) + "/" + escapedKey, nil
	}
	if cfg.Region == "" {
		return "", fmt.Errorf("s3 region is required")
	}
	return "https://" + bucket + ".s3." + cfg.Region + ".amazonaws.com/" + escapedKey, nil
}

func signS3Request(req *http.Request, cfg s3ShipConfig, payloadHash string) {
	host := req.URL.Host
	req.Header.Set("Host", host)
	amzDate := req.Header.Get("X-Amz-Date")
	dateStamp := amzDate[:8]
	canonicalHeaders, signedHeaders := canonicalS3Headers(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		canonicalS3Query(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	scope := dateStamp + "/" + cfg.Region + "/s3/aws4_request"
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	signingKey := s3SigningKey(cfg.SecretAccessKey, dateStamp, cfg.Region)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}

func canonicalS3Headers(req *http.Request) (string, string) {
	values := map[string]string{"host": req.URL.Host}
	for name, headerValues := range req.Header {
		lower := strings.ToLower(name)
		normalized := strings.Join(headerValues, ",")
		normalized = strings.Join(strings.Fields(normalized), " ")
		values[lower] = strings.TrimSpace(normalized)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		builder.WriteString(name)
		builder.WriteString(":")
		builder.WriteString(values[name])
		builder.WriteString("\n")
	}
	return builder.String(), strings.Join(names, ";")
}

func canonicalS3Query(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	values := u.Query()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, key := range keys {
		sort.Strings(values[key])
		for _, value := range values[key] {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

func s3SigningKey(secret string, dateStamp string, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "s3")
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func escapeS3Key(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func joinS3Key(parts ...string) string {
	joined := []string{}
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			joined = append(joined, part)
		}
	}
	return strings.Join(joined, "/")
}

func contentTypeForBundleFile(name string) string {
	switch name {
	case "manifest.json":
		return "application/json"
	case "manifest.sha256":
		return "text/plain"
	default:
		return "application/x-ndjson"
	}
}

func addMultipartFile(writer *multipart.Writer, fieldName string, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", path)
	}
	part, err := writer.CreateFormFile(fieldName, filepath.Base(path))
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(part, file)
	return err
}

func readManifestSHA(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("manifest sha256 file is empty")
	}
	return fields[0], nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeJSONFileExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func copyRegularFileExclusive(source string, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
