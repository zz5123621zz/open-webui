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
	if got := cfg.Provider.DedicatedImageModels["grok-4.5"]; got != "grok-imagine-image-quality" {
		t.Fatalf("default Grok image route = %q", got)
	}
	if cfg.Provider.ImagePromptMaxBytes != 8000 {
		t.Fatalf("image prompt limit = %d, want 8000", cfg.Provider.ImagePromptMaxBytes)
	}
	if cfg.Provider.DefaultReasoningEffort != "high" {
		t.Fatalf("default reasoning effort = %q, want high", cfg.Provider.DefaultReasoningEffort)
	}
	if cfg.Provider.ProgressiveSummaryHardDisabled {
		t.Fatal("progressive summary hard disable defaults to true")
	}
	if cfg.Lifecycle.MaxStorageBytes != 3*1024*1024*1024 ||
		cfg.Lifecycle.MaxActiveConversations != 30 ||
		cfg.Lifecycle.MaxPinnedConversations != 10 {
		t.Fatalf("lifecycle defaults = %#v", cfg.Lifecycle)
	}
}

func TestLoadParsesProgressiveSummaryHardDisable(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AI_PROGRESSIVE_SUMMARY_HARD_DISABLED", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Provider.ProgressiveSummaryHardDisabled {
		t.Fatal("progressive summary hard disable = false")
	}
}

func TestLoadConfiguresVolcengineSpeechFromSecretFile(t *testing.T) {
	apiKeyPath := filepath.Join(t.TempDir(), "volcengine-api-key")
	if err := os.WriteFile(apiKeyPath, []byte("volcengine-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("TTS_VOLCENGINE_API_KEY_FILE", apiKeyPath)
	t.Setenv(
		"TTS_VOLCENGINE_VOICES",
		"zh_female_tianmeitaozi_mars_bigtts:甜美桃子,custom_voice:自定义",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Speech.Volcengine.APIKey != "volcengine-secret" ||
		cfg.Speech.Volcengine.ResourceID != "seed-tts-2.0" ||
		cfg.Speech.Volcengine.Endpoint.String() !=
			"wss://openspeech.bytedance.com/api/v3/tts/bidirection" ||
		len(cfg.Speech.Volcengine.Voices) != 2 {
		t.Fatalf("Volcengine speech config = %#v", cfg.Speech.Volcengine)
	}
}

func TestLoadRejectsUnknownVolcengineSpeechResource(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("TTS_VOLCENGINE_RESOURCE_ID", "unknown-resource")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid Volcengine resource error")
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

func TestLoadParsesImageModelRoutes(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AI_DEFAULT_MODEL", "gpt-default")
	t.Setenv("AI_RESPONSE_IMAGE_MODELS", "gpt-one,gpt-two")
	t.Setenv("AI_DEDICATED_IMAGE_MODELS_JSON", `{"grok-chat":"grok-image"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.Provider.ResponseImageModels); got != 2 {
		t.Fatalf("response image models = %#v", cfg.Provider.ResponseImageModels)
	}
	if got := cfg.Provider.DedicatedImageModels["grok-chat"]; got != "grok-image" {
		t.Fatalf("dedicated image route = %q", got)
	}
}

func TestLoadParsesImagePromptLimit(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AI_IMAGE_PROMPT_MAX_BYTES", "12000")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.ImagePromptMaxBytes != 12000 {
		t.Fatalf("image prompt limit = %d", cfg.Provider.ImagePromptMaxBytes)
	}
}

func TestLoadRejectsInvalidImageModelRoutes(t *testing.T) {
	t.Setenv("APP_SECRET", "01234567890123456789012345678901")
	t.Setenv("AI_API_KEY", "test-provider-api-key-32-bytes!")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AI_DEDICATED_IMAGE_MODELS_JSON", `{"grok-chat":""}`)

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid image route error")
	}
}
