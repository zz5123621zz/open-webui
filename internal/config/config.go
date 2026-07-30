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
	Speech            Speech
	Dictation         Dictation
	Jobs              Jobs
	Tools             Tools
	Lifecycle         Lifecycle
}

type Provider struct {
	Kind                           string
	BaseURL                        *url.URL
	APIKey                         string
	DefaultModel                   string
	ModelAllowlist                 []string
	ModelDenylist                  []string
	ModelContextOverrides          map[string]int
	ResponseImageModels            []string
	DedicatedImageModels           map[string]string
	ModelsTimeout                  time.Duration
	DefaultReasoningEffort         string
	UnknownModelContextTokens      int
	ImagePromptMaxBytes            int
	RequestBodyMaxBytes            int64
	RequestTempDir                 string
	ProgressiveSummaryHardDisabled bool
}

type Speech struct {
	MaxConcurrentGlobal  int
	MaxConcurrentPerUser int
	SessionTTL           time.Duration
	Alibaba              AlibabaSpeech
	Volcengine           VolcengineSpeech
}

type AlibabaSpeech struct {
	Endpoint        *url.URL
	TokenEndpoint   *url.URL
	AppKey          string
	AccessKeyID     string
	AccessKeySecret string
	Voices          []SpeechVoice
	Format          string
	SampleRate      int
}

type VolcengineSpeech struct {
	Endpoint   *url.URL
	APIKey     string
	ResourceID string
	Voices     []SpeechVoice
	Format     string
	SampleRate int
}

type SpeechVoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type Dictation struct {
	MaxConcurrentGlobal  int
	MaxConcurrentPerUser int
	MaxDuration          time.Duration
	SessionTTL           time.Duration
	Volcengine           VolcengineDictation
}

type VolcengineDictation struct {
	Endpoint   *url.URL
	APIKey     string
	ResourceID string
	Format     string
	SampleRate int
	Bits       int
	Channels   int
}

const defaultVolcengineVoiceList = "" +
	"zh_female_vv_uranus_bigtts:Vivi 2.0（女声·中英）," +
	"zh_female_xiaohe_uranus_bigtts:小何（女声·中文）," +
	"zh_male_m191_uranus_bigtts:云舟（男声·中文）," +
	"zh_male_taocheng_uranus_bigtts:小天（男声·中文）," +
	"zh_male_dayi_saturn_bigtts:大壹（男声·视频配音）," +
	"zh_female_mizai_saturn_bigtts:黑猫侦探社咪仔（女声·视频配音）," +
	"zh_female_jitangnv_saturn_bigtts:鸡汤女（女声·视频配音）," +
	"zh_female_meilinvyou_saturn_bigtts:魅力女友（女声·视频配音）," +
	"zh_female_santongyongns_saturn_bigtts:流畅女声（女声·视频配音）," +
	"zh_male_ruyayichen_saturn_bigtts:儒雅逸辰（男声·视频配音）," +
	"zh_female_xueayi_saturn_bigtts:儿童绘本（女声·有声阅读）," +
	"saturn_zh_female_cancan_tob:知性灿灿（女声·角色）," +
	"saturn_zh_female_keainvsheng_tob:可爱女生（女声·角色）," +
	"saturn_zh_female_tiaopigongzhu_tob:调皮公主（女声·角色）," +
	"saturn_zh_male_shuanglangshaonian_tob:爽朗少年（男声·角色）," +
	"saturn_zh_male_tiancaitongzhuo_tob:天才同桌（男声·角色）," +
	"en_male_tim_uranus_bigtts:Tim（男声·英文）," +
	"en_female_dacey_uranus_bigtts:Dacey（女声·英文）," +
	"en_female_stokie_uranus_bigtts:Stokie（女声·英文）"

func defaultVolcengineVoices() []SpeechVoice {
	return []SpeechVoice{
		{ID: "zh_female_vv_uranus_bigtts", Label: "Vivi 2.0（女声·中英）"},
		{ID: "zh_female_xiaohe_uranus_bigtts", Label: "小何（女声·中文）"},
		{ID: "zh_male_m191_uranus_bigtts", Label: "云舟（男声·中文）"},
		{ID: "zh_male_taocheng_uranus_bigtts", Label: "小天（男声·中文）"},
		{ID: "zh_male_dayi_saturn_bigtts", Label: "大壹（男声·视频配音）"},
		{ID: "zh_female_mizai_saturn_bigtts", Label: "黑猫侦探社咪仔（女声·视频配音）"},
		{ID: "zh_female_jitangnv_saturn_bigtts", Label: "鸡汤女（女声·视频配音）"},
		{ID: "zh_female_meilinvyou_saturn_bigtts", Label: "魅力女友（女声·视频配音）"},
		{ID: "zh_female_santongyongns_saturn_bigtts", Label: "流畅女声（女声·视频配音）"},
		{ID: "zh_male_ruyayichen_saturn_bigtts", Label: "儒雅逸辰（男声·视频配音）"},
		{ID: "zh_female_xueayi_saturn_bigtts", Label: "儿童绘本（女声·有声阅读）"},
		{ID: "saturn_zh_female_cancan_tob", Label: "知性灿灿（女声·角色）"},
		{ID: "saturn_zh_female_keainvsheng_tob", Label: "可爱女生（女声·角色）"},
		{ID: "saturn_zh_female_tiaopigongzhu_tob", Label: "调皮公主（女声·角色）"},
		{ID: "saturn_zh_male_shuanglangshaonian_tob", Label: "爽朗少年（男声·角色）"},
		{ID: "saturn_zh_male_tiancaitongzhuo_tob", Label: "天才同桌（男声·角色）"},
		{ID: "en_male_tim_uranus_bigtts", Label: "Tim（男声·英文）"},
		{ID: "en_female_dacey_uranus_bigtts", Label: "Dacey（女声·英文）"},
		{ID: "en_female_stokie_uranus_bigtts", Label: "Stokie（女声·英文）"},
	}
}

func (s Speech) Normalized() Speech {
	if s.MaxConcurrentGlobal <= 0 {
		s.MaxConcurrentGlobal = 2
	}
	if s.MaxConcurrentPerUser <= 0 {
		s.MaxConcurrentPerUser = 1
	}
	if s.SessionTTL <= 0 {
		s.SessionTTL = 30 * time.Minute
	}
	if s.Alibaba.Format == "" {
		s.Alibaba.Format = "pcm"
	}
	if s.Alibaba.SampleRate == 0 {
		s.Alibaba.SampleRate = 24000
	}
	if len(s.Alibaba.Voices) == 0 {
		s.Alibaba.Voices = []SpeechVoice{{ID: "longxiaochun", Label: "龙小淳"}}
	}
	if s.Volcengine.Format == "" {
		s.Volcengine.Format = "pcm"
	}
	if s.Volcengine.SampleRate == 0 {
		s.Volcengine.SampleRate = 24000
	}
	if s.Volcengine.ResourceID == "" {
		s.Volcengine.ResourceID = "seed-tts-2.0"
	}
	if len(s.Volcengine.Voices) == 0 {
		s.Volcengine.Voices = defaultVolcengineVoices()
	}
	return s
}

func (d Dictation) Normalized() Dictation {
	if d.MaxConcurrentGlobal <= 0 {
		d.MaxConcurrentGlobal = 2
	}
	if d.MaxConcurrentPerUser <= 0 {
		d.MaxConcurrentPerUser = 1
	}
	if d.MaxDuration <= 0 {
		d.MaxDuration = 2 * time.Minute
	}
	if d.SessionTTL <= d.MaxDuration {
		d.SessionTTL = d.MaxDuration + 15*time.Second
	}
	if d.Volcengine.Format == "" {
		d.Volcengine.Format = "pcm"
	}
	if d.Volcengine.SampleRate == 0 {
		d.Volcengine.SampleRate = 16000
	}
	if d.Volcengine.Bits == 0 {
		d.Volcengine.Bits = 16
	}
	if d.Volcengine.Channels == 0 {
		d.Volcengine.Channels = 1
	}
	if d.Volcengine.ResourceID == "" {
		d.Volcengine.ResourceID = "volc.seedasr.sauc.duration"
	}
	return d
}

type Jobs struct {
	MaxConcurrentGlobal  int
	MaxConcurrentPerUser int
	MaxQueuedPerUser     int
	QueueTimeout         time.Duration
}

type Tools struct {
	WebSearchEnabled          bool
	ImageGenerationEnabled    bool
	RestaurantGuidanceEnabled bool
}

type Lifecycle struct {
	MaxStorageBytes        int64
	MaxActiveConversations int
	MaxPinnedConversations int
	RetentionTTL           time.Duration
	MaintenanceInterval    time.Duration
}

func (l Lifecycle) Normalized() Lifecycle {
	if l.MaxStorageBytes <= 0 {
		l.MaxStorageBytes = 3 * 1024 * 1024 * 1024
	}
	if l.MaxActiveConversations <= 0 {
		l.MaxActiveConversations = 30
	}
	if l.MaxPinnedConversations <= 0 {
		l.MaxPinnedConversations = 10
	}
	if l.MaxPinnedConversations > l.MaxActiveConversations {
		l.MaxPinnedConversations = l.MaxActiveConversations
	}
	if l.RetentionTTL <= 0 {
		l.RetentionTTL = 7 * 24 * time.Hour
	}
	if l.MaintenanceInterval <= 0 {
		l.MaintenanceInterval = time.Hour
	}
	return l
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
		DefaultReasoningEffort:    strings.ToLower(env("AI_DEFAULT_REASONING_EFFORT", "high")),
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
	cfg.Provider.ProgressiveSummaryHardDisabled, err = boolEnv(
		"AI_PROGRESSIVE_SUMMARY_HARD_DISABLED", false,
	)
	if err != nil {
		return Config{}, err
	}
	providerSecret, err := readSecret("AI_API_KEY", "AI_API_KEY_FILE")
	if err != nil {
		return Config{}, err
	}
	cfg.Provider.APIKey = string(providerSecret)
	speechEndpoint, err := parseAbsoluteURL(
		"TTS_ALIYUN_ENDPOINT",
		"wss://nls-gateway-cn-beijing.aliyuncs.com/ws/v1",
		[]string{"ws", "wss"},
	)
	if err != nil {
		return Config{}, err
	}
	speechTokenEndpoint, err := parseAbsoluteURL(
		"TTS_ALIYUN_TOKEN_ENDPOINT",
		"https://nls-meta.cn-shanghai.aliyuncs.com/",
		[]string{"http", "https"},
	)
	if err != nil {
		return Config{}, err
	}
	accessKeyID, err := readOptionalSecret(
		"TTS_ALIYUN_ACCESS_KEY_ID", "TTS_ALIYUN_ACCESS_KEY_ID_FILE",
	)
	if err != nil {
		return Config{}, err
	}
	accessKeySecret, err := readOptionalSecret(
		"TTS_ALIYUN_ACCESS_KEY_SECRET", "TTS_ALIYUN_ACCESS_KEY_SECRET_FILE",
	)
	if err != nil {
		return Config{}, err
	}
	speechTTLSeconds, err := intEnv("TTS_SESSION_TTL_SECONDS", 1800, 30, 3600)
	if err != nil {
		return Config{}, err
	}
	speechVoices, err := parseSpeechVoices(
		"TTS_ALIYUN_VOICES",
		env("TTS_ALIYUN_VOICES", "longxiaochun:龙小淳"),
	)
	if err != nil {
		return Config{}, err
	}
	volcengineEndpoint, err := parseAbsoluteURL(
		"TTS_VOLCENGINE_ENDPOINT",
		"wss://openspeech.bytedance.com/api/v3/tts/bidirection",
		[]string{"ws", "wss"},
	)
	if err != nil {
		return Config{}, err
	}
	volcengineAPIKey, err := readOptionalSecret(
		"TTS_VOLCENGINE_API_KEY", "TTS_VOLCENGINE_API_KEY_FILE",
	)
	if err != nil {
		return Config{}, err
	}
	volcengineResourceID := env("TTS_VOLCENGINE_RESOURCE_ID", "seed-tts-2.0")
	if volcengineResourceID != "seed-tts-2.0" {
		return Config{}, fmt.Errorf(
			"TTS_VOLCENGINE_RESOURCE_ID must be seed-tts-2.0",
		)
	}
	volcengineVoices, err := parseSpeechVoices(
		"TTS_VOLCENGINE_VOICES",
		env(
			"TTS_VOLCENGINE_VOICES",
			defaultVolcengineVoiceList,
		),
	)
	if err != nil {
		return Config{}, err
	}
	cfg.Speech = Speech{
		MaxConcurrentGlobal:  2,
		MaxConcurrentPerUser: 1,
		SessionTTL:           time.Duration(speechTTLSeconds) * time.Second,
		Alibaba: AlibabaSpeech{
			Endpoint:        speechEndpoint,
			TokenEndpoint:   speechTokenEndpoint,
			AppKey:          strings.TrimSpace(os.Getenv("TTS_ALIYUN_APP_KEY")),
			AccessKeyID:     string(accessKeyID),
			AccessKeySecret: string(accessKeySecret),
			Voices:          speechVoices,
			Format:          "pcm",
			SampleRate:      24000,
		},
		Volcengine: VolcengineSpeech{
			Endpoint:   volcengineEndpoint,
			APIKey:     string(volcengineAPIKey),
			ResourceID: volcengineResourceID,
			Voices:     volcengineVoices,
			Format:     "pcm",
			SampleRate: 24000,
		},
	}
	dictationEndpoint, err := parseAbsoluteURL(
		"ASR_VOLCENGINE_ENDPOINT",
		"wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async",
		[]string{"ws", "wss"},
	)
	if err != nil {
		return Config{}, err
	}
	dictationAPIKey, err := readOptionalSecret(
		"ASR_VOLCENGINE_API_KEY", "ASR_VOLCENGINE_API_KEY_FILE",
	)
	if err != nil {
		return Config{}, err
	}
	dictationResourceID := env(
		"ASR_VOLCENGINE_RESOURCE_ID",
		"volc.seedasr.sauc.duration",
	)
	if dictationResourceID != "volc.seedasr.sauc.duration" {
		return Config{}, fmt.Errorf(
			"ASR_VOLCENGINE_RESOURCE_ID must be volc.seedasr.sauc.duration",
		)
	}
	dictationTTLSeconds, err := intEnv(
		"ASR_SESSION_TTL_SECONDS", 135, 125, 180,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.Dictation = Dictation{
		MaxConcurrentGlobal:  2,
		MaxConcurrentPerUser: 1,
		MaxDuration:          2 * time.Minute,
		SessionTTL:           time.Duration(dictationTTLSeconds) * time.Second,
		Volcengine: VolcengineDictation{
			Endpoint: dictationEndpoint, APIKey: string(dictationAPIKey),
			ResourceID: dictationResourceID, Format: "pcm",
			SampleRate: 16000, Bits: 16, Channels: 1,
		},
	}
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
	cfg.Tools.RestaurantGuidanceEnabled, err = boolEnv(
		"RESTAURANT_GUIDANCE_ENABLED", false,
	)
	if err != nil {
		return Config{}, err
	}
	maxStorageBytes, err := int64Env(
		"USER_MAX_STORAGE_BYTES", 3*1024*1024*1024, 64*1024*1024, 1024*1024*1024*1024,
	)
	if err != nil {
		return Config{}, err
	}
	maxActiveConversations, err := intEnv("USER_MAX_ACTIVE_CONVERSATIONS", 30, 1, 200)
	if err != nil {
		return Config{}, err
	}
	maxPinnedConversations, err := intEnv("USER_MAX_PINNED_CONVERSATIONS", 10, 0, 100)
	if err != nil {
		return Config{}, err
	}
	if maxPinnedConversations > maxActiveConversations {
		return Config{}, fmt.Errorf("USER_MAX_PINNED_CONVERSATIONS cannot exceed active conversation limit")
	}
	retentionHours, err := intEnv("CONVERSATION_RETENTION_HOURS", 7*24, 24, 24*365)
	if err != nil {
		return Config{}, err
	}
	maintenanceMinutes, err := intEnv("MAINTENANCE_INTERVAL_MINUTES", 60, 5, 24*60)
	if err != nil {
		return Config{}, err
	}
	cfg.Lifecycle = Lifecycle{
		MaxStorageBytes:        maxStorageBytes,
		MaxActiveConversations: maxActiveConversations,
		MaxPinnedConversations: maxPinnedConversations,
		RetentionTTL:           time.Duration(retentionHours) * time.Hour,
		MaintenanceInterval:    time.Duration(maintenanceMinutes) * time.Minute,
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

func readOptionalSecret(valueEnv, fileEnv string) ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv(fileEnv)); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", fileEnv, err)
		}
		return []byte(strings.TrimSpace(string(raw))), nil
	}
	return []byte(strings.TrimSpace(os.Getenv(valueEnv))), nil
}

func parseAbsoluteURL(name, fallback string, schemes []string) (*url.URL, error) {
	value, err := url.Parse(env(name, fallback))
	if err != nil || value.Host == "" {
		return nil, fmt.Errorf("%s must be an absolute URL", name)
	}
	for _, scheme := range schemes {
		if value.Scheme == scheme {
			return value, nil
		}
	}
	return nil, fmt.Errorf("%s has an unsupported URL scheme", name)
}

func parseSpeechVoices(name, raw string) ([]SpeechVoice, error) {
	entries := splitCSV(raw)
	if len(entries) == 0 || len(entries) > 32 {
		return nil, fmt.Errorf("%s must contain between 1 and 32 voices", name)
	}
	voices := make([]SpeechVoice, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		id, label, hasLabel := strings.Cut(entry, ":")
		id = strings.TrimSpace(id)
		label = strings.TrimSpace(label)
		if !hasLabel || label == "" {
			label = id
		}
		if id == "" || len(id) > 100 || len(label) > 100 {
			return nil, fmt.Errorf("%s contains an invalid entry", name)
		}
		for _, current := range id {
			if current >= 'a' && current <= 'z' || current >= 'A' && current <= 'Z' ||
				current >= '0' && current <= '9' || current == '-' || current == '_' {
				continue
			}
			return nil, fmt.Errorf("%s contains an invalid voice ID", name)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("%s contains a duplicate voice", name)
		}
		seen[id] = struct{}{}
		voices = append(voices, SpeechVoice{ID: id, Label: label})
	}
	return voices, nil
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

func int64Env(name string, fallback, minimum, maximum int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
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
