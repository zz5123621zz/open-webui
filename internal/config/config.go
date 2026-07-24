package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment       string
	HTTPAddr          string
	BaseURL           *url.URL
	DataDir           string
	DatabasePath      string
	AppSecret         []byte
	SessionTTL        time.Duration
	SessionCookieName string
	SecureCookies     bool
	Provider          Provider
	Jobs              Jobs
	Tools             Tools
}

type Provider struct {
	Kind                      string
	BaseURL                   *url.URL
	APIKey                    string
	DefaultModel              string
	ModelAllowlist            []string
	ModelDenylist             []string
	ModelContextOverrides     map[string]int
	ResponseImageModels       []string
	DedicatedImageModels      map[string]string
	ModelsTimeout             time.Duration
	DefaultReasoningEffort    string
	UnknownModelContextTokens int
	ImagePromptMaxBytes       int
	RequestBodyMaxBytes       int64
	RequestTempDir            string
}

type Jobs struct {
	MaxConcurrentGlobal  int
	MaxConcurrentPerUser int
	MaxQueuedPerUser     int
	QueueTimeout         time.Duration
}

type Tools struct {
	WebSearchEnabled       bool
	ImageGenerationEnabled bool
}

func Load() (Config, error) {
	cfg := Config{
		Environment:       env("APP_ENV", "development"),
		HTTPAddr:          env("HTTP_ADDR", ":8080"),
		DataDir:           env("APP_DATA_DIR", "./data"),
		SessionCookieName: env("SESSION_COOKIE_NAME", "owui_session"),
		SessionTTL:        30 * 24 * time.Hour,
	}

	baseURL, err := url.Parse(env("APP_BASE_URL", "http://localhost:8080"))
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return Config{}, fmt.Errorf("APP_BASE_URL must be an absolute http(s) URL")
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return Config{}, fmt.Errorf("APP_BASE_URL scheme must be http or https")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/")
	cfg.BaseURL = baseURL
	cfg.SecureCookies = baseURL.Scheme == "https"

	if raw := strings.TrimSpace(os.Getenv("SESSION_TTL_HOURS")); raw != "" {
		hours, parseErr := strconv.Atoi(raw)
		if parseErr != nil || hours < 1 || hours > 24*365 {
			return Config{}, fmt.Errorf("SESSION_TTL_HOURS must be between 1 and 8760")
		}
		cfg.SessionTTL = time.Duration(hours) * time.Hour
	}

	absoluteDataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve APP_DATA_DIR: %w", err)
	}
	cfg.DataDir = absoluteDataDir
	cfg.DatabasePath = filepath.Join(absoluteDataDir, "app.db")

	secret, err := readSecret("APP_SECRET", "APP_SECRET_FILE")
	if err != nil {
		return Config{}, err
	}
	if len(secret) < 32 {
		return Config{}, errors.New("application secret must contain at least 32 bytes")
	}
	cfg.AppSecret = secret

	providerBaseURL, err := url.Parse(env("AI_BASE_URL", "http://localhost:8317/v1"))
	if err != nil || providerBaseURL.Scheme == "" || providerBaseURL.Host == "" {
		return Config{}, fmt.Errorf("AI_BASE_URL must be an absolute http(s) URL")
	}
	providerBaseURL.Path = strings.TrimRight(providerBaseURL.Path, "/")
	cfg.Provider = Provider{
		Kind:                      strings.ToLower(env("AI_PROVIDER", "cpa")),
		BaseURL:                   providerBaseURL,
		DefaultModel:              strings.TrimSpace(os.Getenv("AI_DEFAULT_MODEL")),
		ModelAllowlist:            splitCSV(os.Getenv("AI_MODEL_ALLOWLIST")),
		ModelDenylist:             splitCSV(os.Getenv("AI_MODEL_DENYLIST")),
		ModelContextOverrides:     make(map[string]int),
		DedicatedImageModels:      make(map[string]string),
		ModelsTimeout:             5 * time.Second,
		DefaultReasoningEffort:    strings.ToLower(env("AI_DEFAULT_REASONING_EFFORT", "auto")),
		UnknownModelContextTokens: 128000,
		ImagePromptMaxBytes:       8000,
		RequestBodyMaxBytes:       50 * 1024 * 1024,
		RequestTempDir:            filepath.Join(absoluteDataDir, "tmp", "provider"),
	}
	if cfg.Provider.Kind != "cpa" && cfg.Provider.Kind != "openai" {
		return Config{}, fmt.Errorf("AI_PROVIDER must be cpa or openai")
	}
	responseImageModels := strings.TrimSpace(os.Getenv("AI_RESPONSE_IMAGE_MODELS"))
	if responseImageModels == "" {
		responseImageModels = cfg.Provider.DefaultModel
	}
	cfg.Provider.ResponseImageModels = splitCSV(responseImageModels)
	dedicatedImageModels := strings.TrimSpace(os.Getenv("AI_DEDICATED_IMAGE_MODELS_JSON"))
	if dedicatedImageModels == "" && cfg.Provider.Kind == "cpa" {
		dedicatedImageModels = `{"grok-4.5":"grok-imagine-image-quality"}`
	}
	if dedicatedImageModels != "" {
		if err := json.Unmarshal([]byte(dedicatedImageModels), &cfg.Provider.DedicatedImageModels); err != nil {
			return Config{}, fmt.Errorf("AI_DEDICATED_IMAGE_MODELS_JSON must be a JSON object")
		}
		if len(cfg.Provider.DedicatedImageModels) > 64 {
			return Config{}, fmt.Errorf("AI_DEDICATED_IMAGE_MODELS_JSON contains too many models")
		}
		for chatModel, imageModel := range cfg.Provider.DedicatedImageModels {
			if strings.TrimSpace(chatModel) == "" || strings.TrimSpace(imageModel) == "" ||
				len(chatModel) > 200 || len(imageModel) > 200 {
				return Config{}, fmt.Errorf("AI_DEDICATED_IMAGE_MODELS_JSON contains an invalid entry")
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AI_MODELS_TIMEOUT_SECONDS")); raw != "" {
		seconds, parseErr := strconv.Atoi(raw)
		if parseErr != nil || seconds < 1 || seconds > 30 {
			return Config{}, fmt.Errorf("AI_MODELS_TIMEOUT_SECONDS must be between 1 and 30")
		}
		cfg.Provider.ModelsTimeout = time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("AI_UNKNOWN_MODEL_CONTEXT_TOKENS")); raw != "" {
		tokens, parseErr := strconv.Atoi(raw)
		if parseErr != nil || tokens < 8192 || tokens > 4_000_000 {
			return Config{}, fmt.Errorf("AI_UNKNOWN_MODEL_CONTEXT_TOKENS must be between 8192 and 4000000")
		}
		cfg.Provider.UnknownModelContextTokens = tokens
	}
	if raw := strings.TrimSpace(os.Getenv("AI_MODEL_CONTEXT_OVERRIDES_JSON")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.Provider.ModelContextOverrides); err != nil {
			return Config{}, fmt.Errorf("AI_MODEL_CONTEXT_OVERRIDES_JSON must be a JSON object")
		}
		if len(cfg.Provider.ModelContextOverrides) > 256 {
			return Config{}, fmt.Errorf("AI_MODEL_CONTEXT_OVERRIDES_JSON contains too many models")
		}
		for modelID, tokens := range cfg.Provider.ModelContextOverrides {
			if strings.TrimSpace(modelID) == "" || tokens < 8192 || tokens > 4_000_000 {
				return Config{}, fmt.Errorf("AI_MODEL_CONTEXT_OVERRIDES_JSON contains an invalid entry")
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AI_REQUEST_BODY_MAX_BYTES")); raw != "" {
		bytes, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || bytes < 1<<20 || bytes > 50*1024*1024 {
			return Config{}, fmt.Errorf("AI_REQUEST_BODY_MAX_BYTES must be between 1 MiB and 50 MiB")
		}
		cfg.Provider.RequestBodyMaxBytes = bytes
	}
	if raw := strings.TrimSpace(os.Getenv("AI_IMAGE_PROMPT_MAX_BYTES")); raw != "" {
		bytes, parseErr := strconv.Atoi(raw)
		if parseErr != nil || bytes < 1024 || bytes > 1<<20 {
			return Config{}, fmt.Errorf("AI_IMAGE_PROMPT_MAX_BYTES must be between 1024 and 1048576")
		}
		cfg.Provider.ImagePromptMaxBytes = bytes
	}
	if !validReasoningEffort(cfg.Provider.DefaultReasoningEffort) {
		return Config{}, fmt.Errorf("AI_DEFAULT_REASONING_EFFORT is invalid")
	}
	providerSecret, err := readSecret("AI_API_KEY", "AI_API_KEY_FILE")
	if err != nil {
		return Config{}, err
	}
	cfg.Provider.APIKey = string(providerSecret)
	globalLimit, err := intEnv("PROVIDER_MAX_CONCURRENT_GLOBAL", 4, 1, 32)
	if err != nil {
		return Config{}, err
	}
	perUserLimit, err := intEnv("PROVIDER_MAX_CONCURRENT_PER_USER", 2, 1, 8)
	if err != nil {
		return Config{}, err
	}
	perUserQueueLimit, err := intEnv("PROVIDER_MAX_QUEUED_PER_USER", 2, 0, 16)
	if err != nil {
		return Config{}, err
	}
	queueTimeoutSeconds, err := intEnv("PROVIDER_QUEUE_TIMEOUT_SECONDS", 60, 1, 600)
	if err != nil {
		return Config{}, err
	}
	cfg.Jobs = Jobs{
		MaxConcurrentGlobal:  globalLimit,
		MaxConcurrentPerUser: perUserLimit,
		MaxQueuedPerUser:     perUserQueueLimit,
		QueueTimeout:         time.Duration(queueTimeoutSeconds) * time.Second,
	}
	if cfg.Jobs.MaxConcurrentPerUser > cfg.Jobs.MaxConcurrentGlobal {
		return Config{}, fmt.Errorf("PROVIDER_MAX_CONCURRENT_PER_USER cannot exceed global limit")
	}
	cfg.Tools.WebSearchEnabled, err = boolEnv("TOOL_WEB_SEARCH_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	cfg.Tools.ImageGenerationEnabled, err = boolEnv("TOOL_IMAGE_GENERATION_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func readSecret(valueEnv, fileEnv string) ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv(fileEnv)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fileEnv, err)
		}
		if secret := strings.TrimSpace(string(raw)); secret != "" {
			return []byte(secret), nil
		}
		return nil, fmt.Errorf("%s is empty", fileEnv)
	}
	if value := strings.TrimSpace(os.Getenv(valueEnv)); value != "" {
		return []byte(value), nil
	}
	return nil, fmt.Errorf("%s or %s is required", valueEnv, fileEnv)
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func validReasoningEffort(value string) bool {
	switch value {
	case "auto", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return true
	default:
		return false
	}
}

func intEnv(name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minimum, maximum)
	}
	return value, nil
}

func boolEnv(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return value, nil
}
