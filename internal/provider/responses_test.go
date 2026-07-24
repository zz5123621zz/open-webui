package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
)

func TestStartResponseStreamsImageThroughTemporaryRequestFile(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "pixel.png")
	imageBytes := []byte{0x89, 'P', 'N', 'G', 1, 2, 3, 4}
	if err := os.WriteFile(imagePath, imageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	var contentLength int64
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentLength = r.ContentLength
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	baseURL, _ := url.Parse(upstream.URL + "/v1")
	client := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: 1 << 20,
		RequestTempDir: tempDir,
	}, "test")
	request := ResponsesRequest{
		Model: "gpt-test", Instructions: "Keep visible summaries in the user's language.",
		SafetyIdentifier: "stable-pseudonymous-user",
		Stream: true, Store: false,
		Input: []ResponseInput{{
			Role: "user",
			Content: []ResponseContent{
				{Type: "input_text", Text: "describe"},
				{
					Type: "input_image", ImagePath: imagePath, MediaType: "image/png",
					ByteSize: int64(len(imageBytes)),
				},
			},
		}},
		Reasoning: ReasoningOptions{Summary: "auto"},
	}
	response, err := client.StartResponse(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if contentLength <= 0 {
		t.Fatalf("Content-Length = %d", contentLength)
	}
	if received["safety_identifier"] != "stable-pseudonymous-user" {
		t.Fatalf("safety_identifier = %#v", received["safety_identifier"])
	}
	if received["instructions"] != request.Instructions {
		t.Fatalf("instructions = %#v", received["instructions"])
	}
	input := received["input"].([]any)[0].(map[string]any)
	content := input["content"].([]any)
	imageURL := content[1].(map[string]any)["image_url"].(string)
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("image_url = %q", imageURL)
	}
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary request files remain: %#v", entries)
	}
}

func TestStartResponseEnforcesCompiledBodyLimit(t *testing.T) {
	baseURL, _ := url.Parse("http://127.0.0.1:1/v1")
	client := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: 64,
		RequestTempDir: t.TempDir(),
	}, "test")
	_, err := client.StartResponse(context.Background(), ResponsesRequest{
		Model:     "gpt-test",
		Input:     []ResponseInput{{Role: "user", Content: strings.Repeat("x", 200)}},
		Reasoning: ReasoningOptions{Summary: "auto"},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds 64 bytes") {
		t.Fatalf("StartResponse() error = %v", err)
	}
}

func TestCompileRequestEnforcesExactBodyBoundary(t *testing.T) {
	baseURL, _ := url.Parse("http://127.0.0.1:1/v1")
	request := ResponsesRequest{
		Model:     "gpt-test",
		Input:     []ResponseInput{{Role: "user", Content: "exact boundary"}},
		Reasoning: ReasoningOptions{Summary: "auto"},
		Stream:    true,
		Store:     false,
	}
	unlimited := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: 1 << 20,
		RequestTempDir: t.TempDir(),
	}, "test")
	body, bodyPath, size, err := unlimited.compileRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = body.Close()
	_ = os.Remove(bodyPath)

	exact := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: size,
		RequestTempDir: t.TempDir(),
	}, "test")
	body, bodyPath, _, err = exact.compileRequest(request)
	if err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	_ = body.Close()
	_ = os.Remove(bodyPath)

	tooSmall := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: size - 1,
		RequestTempDir: t.TempDir(),
	}, "test")
	if _, _, _, err := tooSmall.compileRequest(request); !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("boundary + 1 error = %v, want %v", err, errRequestTooLarge)
	}
}

func TestStartResponseReplaysRawProviderItemsInOrder(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	baseURL, _ := url.Parse(upstream.URL + "/v1")
	client := NewClient(config.Provider{
		BaseURL: baseURL, APIKey: "test", RequestBodyMaxBytes: 1 << 20,
		RequestTempDir: t.TempDir(),
	}, "test")
	response, err := client.StartResponse(context.Background(), ResponsesRequest{
		Model: "gpt-test",
		Input: []ResponseInput{
			{Role: "user", Content: "hello"},
			{Raw: json.RawMessage(`{"type":"reasoning","id":"reasoning-1","encrypted_content":"opaque"}`)},
			{Raw: json.RawMessage(`{"type":"message","id":"message-1","role":"assistant","content":[{"type":"output_text","text":"hi"}]}`)},
		},
		Reasoning: ReasoningOptions{Summary: "auto"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	input := received["input"].([]any)
	if len(input) != 3 || input[1].(map[string]any)["type"] != "reasoning" ||
		input[2].(map[string]any)["id"] != "message-1" {
		t.Fatalf("raw replay input = %#v", input)
	}
}
