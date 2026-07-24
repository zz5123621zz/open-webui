package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

const (
	maxModelsResponseBytes = 1 << 20
	maxModels              = 256
)

var (
	ErrUnavailable = errors.New("provider unavailable")
	ErrBadResponse = errors.New("invalid provider response")
)

type Model struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	Description            string   `json:"description,omitempty"`
	ContextWindow          int      `json:"contextWindow"`
	InputModalities        []string `json:"inputModalities,omitempty"`
	SupportsWebSearch      bool     `json:"supportsWebSearch"`
	ImageGenerationMode    string   `json:"imageGenerationMode,omitempty"`
	ReasoningEfforts       []string `json:"reasoningEfforts,omitempty"`
	DefaultReasoningEffort string   `json:"defaultReasoningEffort,omitempty"`
	CapabilitiesComplete   bool     `json:"capabilitiesComplete"`
	Selectable             bool     `json:"selectable"`
	DedicatedImageModel    string   `json:"-"`
	Priority               int      `json:"-"`
}

type Catalog struct {
	Models []Model `json:"models"`
}

type Client struct {
	baseURL          *url.URL
	apiKey           string
	defaultModel     string
	allowlist        map[string]struct{}
	denylist         map[string]struct{}
	unknownCtx       int
	contextOverrides map[string]int
	responseImages    map[string]struct{}
	dedicatedImages   map[string]string
	httpClient       *http.Client
	streamClient     *http.Client
	clientVersion    string
	requestMax       int64
	requestTempDir   string
}

func NewClient(cfg config.Provider, appVersion string) *Client {
	responseImageModels := cfg.ResponseImageModels
	if len(responseImageModels) == 0 && strings.TrimSpace(cfg.DefaultModel) != "" {
		responseImageModels = []string{cfg.DefaultModel}
	}
	return &Client{
		baseURL:          cfg.BaseURL,
		apiKey:           cfg.APIKey,
		defaultModel:     cfg.DefaultModel,
		allowlist:        stringSet(cfg.ModelAllowlist),
		denylist:         stringSet(cfg.ModelDenylist),
		unknownCtx:       cfg.UnknownModelContextTokens,
		contextOverrides: cloneIntMap(cfg.ModelContextOverrides),
		responseImages:    stringSet(responseImageModels),
		dedicatedImages:   cloneStringMap(cfg.DedicatedImageModels),
		httpClient:       &http.Client{Timeout: cfg.ModelsTimeout},
		streamClient: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			// CPA image generation may not return response headers until the
			// image is complete. Match the 600-second Nginx boundary while the
			// request context still handles explicit cancellation.
			ResponseHeaderTimeout: 10 * time.Minute,
		}},
		clientVersion:  "owui-personal-slim/" + appVersion,
		requestMax:     cfg.RequestBodyMaxBytes,
		requestTempDir: cfg.RequestTempDir,
	}
}

func (c *Client) Models(ctx context.Context) (Catalog, error) {
	enhancedURL := *c.baseURL
	enhancedURL.Path = strings.TrimRight(enhancedURL.Path, "/") + "/models"
	query := enhancedURL.Query()
	query.Set("client_version", c.clientVersion)
	enhancedURL.RawQuery = query.Encode()

	var enhanced enhancedModelsResponse
	if err := c.getJSON(ctx, enhancedURL.String(), &enhanced); err == nil && len(enhanced.Models) > 0 {
		return c.normalizeEnhanced(enhanced.Models), nil
	}

	plainURL := enhancedURL
	plainURL.RawQuery = ""
	var plain plainModelsResponse
	if err := c.getJSON(ctx, plainURL.String(), &plain); err != nil {
		return Catalog{}, err
	}
	if len(plain.Data) == 0 {
		return Catalog{}, fmt.Errorf("%w: empty model catalog", ErrBadResponse)
	}
	return c.normalizePlain(plain.Data), nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return fmt.Errorf("%w: model catalog returned HTTP %d", ErrUnavailable, resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxModelsResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("%w: read model catalog", ErrBadResponse)
	}
	if len(raw) > maxModelsResponseBytes {
		return fmt.Errorf("%w: model catalog exceeds 1 MiB", ErrBadResponse)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("%w: decode model catalog", ErrBadResponse)
	}
	return nil
}

type enhancedModelsResponse struct {
	Models []enhancedModel `json:"models"`
}

type enhancedModel struct {
	Slug                   string           `json:"slug"`
	DisplayName            string           `json:"display_name"`
	Description            string           `json:"description"`
	Visibility             string           `json:"visibility"`
	ContextWindow          int              `json:"context_window"`
	InputModalities        []string         `json:"input_modalities"`
	SupportsSearchTool     bool             `json:"supports_search_tool"`
	SupportedReasoning     []reasoningLevel `json:"supported_reasoning_levels"`
	DefaultReasoningEffort string           `json:"default_reasoning_level"`
	Priority               int              `json:"priority"`
}

type reasoningLevel struct {
	Effort string `json:"effort"`
}

type plainModelsResponse struct {
	Data []plainModel `json:"data"`
}

type plainModel struct {
	ID string `json:"id"`
}

func (c *Client) normalizeEnhanced(input []enhancedModel) Catalog {
	models := make([]Model, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.Slug)
		if id == "" || strings.EqualFold(strings.TrimSpace(item.Visibility), "hide") {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, denied := c.denylist[id]; denied {
			continue
		}
		if len(c.allowlist) > 0 {
			if _, allowed := c.allowlist[id]; !allowed {
				continue
			}
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" {
			name = id
		}
		model := Model{
			ID: id, Name: name, Description: strings.TrimSpace(item.Description),
			ContextWindow: item.ContextWindow, InputModalities: normalizeModalities(item.InputModalities),
			SupportsWebSearch: item.SupportsSearchTool, ReasoningEfforts: normalizeEfforts(item.SupportedReasoning),
			DefaultReasoningEffort: normalizeEffort(item.DefaultReasoningEffort),
			CapabilitiesComplete:   true, Selectable: true, Priority: item.Priority,
		}
		c.applyImageGenerationCapability(&model)
		if override, exists := c.contextOverrides[id]; exists {
			model.ContextWindow = override
		}
		if model.ContextWindow <= 0 {
			model.ContextWindow = c.unknownCtx
			model.CapabilitiesComplete = false
		}
		models = append(models, model)
		if len(models) == maxModels {
			break
		}
	}
	sortModels(models)
	return Catalog{Models: models}
}

func (c *Client) normalizePlain(input []plainModel) Catalog {
	models := make([]Model, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if _, denied := c.denylist[id]; denied {
			continue
		}
		_, explicitlyAllowed := c.allowlist[id]
		selectable := explicitlyAllowed || len(c.allowlist) == 0 && id == c.defaultModel
		contextWindow := c.unknownCtx
		if override, exists := c.contextOverrides[id]; exists {
			contextWindow = override
		}
		model := Model{
			ID: id, Name: id, ContextWindow: contextWindow,
			CapabilitiesComplete: false, Selectable: selectable,
		}
		c.applyImageGenerationCapability(&model)
		models = append(models, model)
		if len(models) == maxModels {
			break
		}
	}
	sortModels(models)
	return Catalog{Models: models}
}

func cloneIntMap(input map[string]int) map[string]int {
	result := make(map[string]int, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return result
}

func (c *Client) applyImageGenerationCapability(model *Model) {
	if imageModel, ok := c.dedicatedImages[model.ID]; ok {
		model.ImageGenerationMode = "dedicated"
		model.DedicatedImageModel = imageModel
		return
	}
	if _, ok := c.responseImages[model.ID]; ok {
		model.ImageGenerationMode = "responses_tool"
	}
}

func (c *Client) FindSelectable(catalog Catalog, id string) (Model, bool) {
	for _, model := range catalog.Models {
		if model.ID == id && model.Selectable {
			return model, true
		}
	}
	return Model{}, false
}

func normalizeModalities(input []string) []string {
	allowed := map[string]bool{"text": true, "image": true}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, value := range input {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeEfforts(input []reasoningLevel) []string {
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		effort := normalizeEffort(item.Effort)
		if effort == "" {
			continue
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		result = append(result, effort)
	}
	return result
}

func normalizeEffort(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
		return value
	default:
		return ""
	}
}

func sortModels(models []Model) {
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Priority != models[j].Priority {
			return models[i].Priority < models[j].Priority
		}
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func SupportsEffort(model Model, effort string) bool {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "auto" {
		return true
	}
	for _, supported := range model.ReasoningEfforts {
		if supported == effort {
			return true
		}
	}
	return false
}

func (c *Client) SetHTTPClient(client *http.Client) {
	if client != nil {
		c.httpClient = client
	}
}

func NewTestConfig(baseURL *url.URL) config.Provider {
	return config.Provider{
		Kind: "cpa", BaseURL: baseURL, APIKey: "test-key", ModelsTimeout: time.Second,
		DefaultModel: "gpt-default", UnknownModelContextTokens: 128000, RequestBodyMaxBytes: 50 << 20,
	}
}
