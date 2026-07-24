package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestModelsUsesEnhancedCatalogAndFiltersHidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if _, ok := r.URL.Query()["client_version"]; !ok {
			t.Fatal("client_version missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[
			{"slug":"gpt-chat","display_name":"GPT Chat","context_window":200000,"input_modalities":["text","image"],"supports_search_tool":true,"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}],"default_reasoning_level":"low","priority":1},
			{"slug":"gpt-image-2","display_name":"Image","visibility":"hide","priority":2}
		]}`))
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	cfg := NewTestConfig(baseURL)
	cfg.ModelContextOverrides = map[string]int{"gpt-chat": 180000}
	cfg.ResponseImageModels = []string{"gpt-chat"}
	client := NewClient(cfg, "test")

	catalog, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("models = %#v", catalog.Models)
	}
	model := catalog.Models[0]
	if model.ID != "gpt-chat" || model.ContextWindow != 180000 ||
		!model.SupportsWebSearch || !SupportsEffort(model, "high") ||
		model.ImageGenerationMode != "responses_tool" {
		t.Fatalf("model = %#v", model)
	}
}

func TestModelsMarksDedicatedImageRouteWithoutExposingModelID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{
			"slug":"grok-4.5","display_name":"Grok 4.5","context_window":131072,
			"input_modalities":["text","image"],"priority":1
		}]}`))
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	cfg := NewTestConfig(baseURL)
	cfg.DedicatedImageModels = map[string]string{
		"grok-4.5": "grok-imagine-image-quality",
	}
	client := NewClient(cfg, "test")

	catalog, err := client.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 {
		t.Fatalf("models = %#v", catalog.Models)
	}
	model := catalog.Models[0]
	if model.ImageGenerationMode != "dedicated" ||
		model.DedicatedImageModel != "grok-imagine-image-quality" {
		t.Fatalf("model = %#v", model)
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "grok-imagine-image-quality") {
		t.Fatalf("public model JSON leaked dedicated model: %s", raw)
	}
}

func TestModelsPlainFallbackOnlySelectsDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, ok := r.URL.Query()["client_version"]; ok {
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-default"},{"id":"other"}]}`))
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	client := NewClient(NewTestConfig(baseURL), "test")

	catalog, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(catalog.Models) != 2 || !catalog.Models[0].Selectable || catalog.Models[1].Selectable {
		t.Fatalf("models = %#v", catalog.Models)
	}
}
