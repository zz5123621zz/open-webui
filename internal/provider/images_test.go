package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

func TestGenerateImageUsesDedicatedEndpointWithoutQualityOverrides(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":123,"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	client := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test-key", RequestBodyMaxBytes: 50 << 20,
	}, "test")

	result, err := client.GenerateImage(context.Background(), ImageGenerationRequest{
		Model: "grok-imagine-image-quality", Prompt: "draw a fox",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "image-123" || result.Base64 != "aW1hZ2U=" {
		t.Fatalf("result = %#v", result)
	}
	if received["model"] != "grok-imagine-image-quality" ||
		received["prompt"] != "draw a fox" ||
		received["response_format"] != "b64_json" ||
		received["n"] != float64(1) {
		t.Fatalf("request = %#v", received)
	}
	for _, forbidden := range []string{"quality", "size", "compression", "partial_images"} {
		if _, exists := received[forbidden]; exists {
			t.Fatalf("request unexpectedly set %s: %#v", forbidden, received)
		}
	}
}

func TestGenerateImageEnforcesResponseBoundary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+string(make([]byte, 256))+`"}]}`)
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	client := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test-key", RequestBodyMaxBytes: 128,
	}, "test")

	if _, err := client.GenerateImage(context.Background(), ImageGenerationRequest{
		Model: "grok-image", Prompt: "fox",
	}); err == nil {
		t.Fatal("GenerateImage() error = nil, want response boundary error")
	}
}
