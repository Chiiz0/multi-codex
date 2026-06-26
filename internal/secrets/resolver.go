package secrets

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

type Resolver interface {
	Provider() string
	Lookup(name string) (string, bool, error)
}

type ResolverConfig struct {
	Provider        string
	FilePath        string
	VaultAddress    string
	VaultToken      string
	VaultTokenFile  string
	VaultNamespace  string
	VaultMount      string
	VaultSecretPath string
	HTTPClient      *http.Client
}

func NewResolver(provider string, filePath string) (Resolver, error) {
	return NewResolverWithConfig(ResolverConfig{Provider: provider, FilePath: filePath})
}

func NewResolverWithConfig(cfg ResolverConfig) (Resolver, error) {
	switch strings.TrimSpace(cfg.Provider) {
	case "", "env":
		return EnvResolver{}, nil
	case "file":
		if strings.TrimSpace(cfg.FilePath) == "" {
			return nil, fmt.Errorf("secret file path is required for file provider")
		}
		return FileResolver{Path: cfg.FilePath}, nil
	case "vault":
		return NewVaultResolver(cfg)
	default:
		return nil, fmt.Errorf("unsupported secret provider %q", cfg.Provider)
	}
}

type EnvResolver struct{}

func (EnvResolver) Provider() string {
	return "env"
}

func (EnvResolver) Lookup(name string) (string, bool, error) {
	value, ok := os.LookupEnv(name)
	return value, ok && value != "", nil
}

type FileResolver struct {
	Path string
}

func (r FileResolver) Provider() string {
	return "file"
}

func (r FileResolver) Lookup(name string) (string, bool, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return "", false, err
	}
	values := map[string]string{}
	if err := json.Unmarshal(data, &values); err != nil {
		return "", false, err
	}
	value, ok := values[name]
	return value, ok && value != "", nil
}

type VaultResolver struct {
	Address    string
	Token      string
	Namespace  string
	Mount      string
	SecretPath string
	Client     *http.Client
}

func NewVaultResolver(cfg ResolverConfig) (VaultResolver, error) {
	address := strings.TrimRight(strings.TrimSpace(cfg.VaultAddress), "/")
	if address == "" {
		return VaultResolver{}, fmt.Errorf("vault address is required")
	}
	token := strings.TrimSpace(cfg.VaultToken)
	if token == "" && strings.TrimSpace(cfg.VaultTokenFile) != "" {
		data, err := os.ReadFile(cfg.VaultTokenFile)
		if err != nil {
			return VaultResolver{}, fmt.Errorf("read vault token file: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		return VaultResolver{}, fmt.Errorf("vault token is required")
	}
	mount := strings.Trim(strings.TrimSpace(cfg.VaultMount), "/")
	if mount == "" {
		mount = "secret"
	}
	secretPath := strings.Trim(strings.TrimSpace(cfg.VaultSecretPath), "/")
	if secretPath == "" {
		return VaultResolver{}, fmt.Errorf("vault secret path is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return VaultResolver{
		Address:    address,
		Token:      token,
		Namespace:  strings.TrimSpace(cfg.VaultNamespace),
		Mount:      mount,
		SecretPath: secretPath,
		Client:     client,
	}, nil
}

func (r VaultResolver) Provider() string {
	return "vault"
}

func (r VaultResolver) Lookup(name string) (string, bool, error) {
	endpoint, err := r.readURL()
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("X-Vault-Token", r.Token)
	if r.Namespace != "" {
		req.Header.Set("X-Vault-Namespace", r.Namespace)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", false, fmt.Errorf("vault read returned status %d", resp.StatusCode)
	}
	var payload struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", false, err
	}
	value, ok := payload.Data.Data[name]
	if !ok || value == nil {
		return "", false, nil
	}
	text, ok := value.(string)
	if !ok {
		text = fmt.Sprint(value)
	}
	return text, strings.TrimSpace(text) != "", nil
}

func (r VaultResolver) readURL() (string, error) {
	parsed, err := url.Parse(r.Address)
	if err != nil {
		return "", err
	}
	parsed.Path = path.Join(parsed.Path, "v1", r.Mount, "data", r.SecretPath)
	return parsed.String(), nil
}

type UnavailableResolver struct {
	ProviderName string
	Err          error
}

func (r UnavailableResolver) Provider() string {
	if strings.TrimSpace(r.ProviderName) == "" {
		return "unavailable"
	}
	return r.ProviderName
}

func (r UnavailableResolver) Lookup(string) (string, bool, error) {
	if r.Err != nil {
		return "", false, r.Err
	}
	return "", false, fmt.Errorf("secret provider is unavailable")
}
