package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDevelopmentConfig(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("APP_BASE_URL", "http://localhost:8080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SecureCookies {
		t.Fatal("SecureCookies = true for http URL")
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Fatalf("DataDir = %q, want absolute", cfg.DataDir)
	}
}

func TestLoadSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("abcdefghijklmnopqrstuvwxyz-123456\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_SECRET", "")
	t.Setenv("APP_SECRET_FILE", path)
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := string(cfg.AppSecret); got != "abcdefghijklmnopqrstuvwxyz-123456" {
		t.Fatalf("AppSecret = %q", got)
	}
}

func TestLoadRejectsShortSecret(t *testing.T) {
	t.Setenv("APP_SECRET_FILE", "")
	t.Setenv("APP_SECRET", "short")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want short-secret error")
	}
}

func TestLoadRejectsInvalidJobLimit(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("PROVIDER_MAX_CONCURRENT_GLOBAL", "many")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid job-limit error")
	}
}

func TestLoadParsesModelContextOverrides(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AI_MODEL_CONTEXT_OVERRIDES_JSON", `{"gpt-test":200000}`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.ModelContextOverrides["gpt-test"] != 200000 {
		t.Fatalf("context overrides = %#v", cfg.Provider.ModelContextOverrides)
	}
}
