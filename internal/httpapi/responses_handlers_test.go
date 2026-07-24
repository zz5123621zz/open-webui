package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestSafeWebActionUsesStrictAllowlist(t *testing.T) {
	raw := safeWebAction(map[string]any{
		"type":          "search",
		"query":         "safe query",
		"url":           "https://user:password@example.com/result",
		"pattern":       "needle",
		"authorization": "Bearer secret",
		"headers":       map[string]string{"X-Secret": "do not expose"},
	})
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got["query"] != "safe query" {
		t.Fatalf("safe action = %#v", got)
	}
	if strings.Contains(got["url"], "password") || strings.Contains(string(raw), "secret") {
		t.Fatalf("safe action leaked credentials: %s", raw)
	}
}

func TestCitationURLRejectsUnsafeSchemes(t *testing.T) {
	for _, value := range []string{
		"javascript:alert(1)",
		"data:text/html,bad",
		"//example.com/no-scheme",
		"https:///missing-host",
	} {
		if got := sanitizeCitationURL(value); got != "" {
			t.Errorf("sanitizeCitationURL(%q) = %q", value, got)
		}
	}
	if got := sanitizeCitationURL("https://user:pass@example.com/path"); got != "https://example.com/path" {
		t.Fatalf("sanitized URL = %q", got)
	}
}

func TestWebSearchLifecycleAndOutputItemAreDeduplicated(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{}
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.web_search_call.searching", ItemID: "search_1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.web_search_call.completed", ItemID: "search_1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.output_item.done",
		Item: json.RawMessage(`{
			"id":"search_1",
			"type":"web_search_call",
			"status":"completed",
			"action":{"type":"search","query":"weather","authorization":"secret"}
		}`),
	}); err != nil {
		t.Fatal(err)
	}
	if len(accumulator.tools) != 1 {
		t.Fatalf("tools = %#v", accumulator.tools)
	}
	if got := string(accumulator.tools[0].Data); !strings.Contains(got, `"query":"weather"`) ||
		strings.Contains(got, "secret") {
		t.Fatalf("tool data = %s", got)
	}
}

func TestImageGenerationDoneNormalizesGeneratingStatusAndSavesResult(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{
		saveImage: func(result string) (generatedImage, error) {
			if result != "final-image-base64" {
				t.Fatalf("image result = %q", result)
			}
			return generatedImage{
				AttachmentID: "attachment_1",
				URL:          "/api/v1/attachments/attachment_1/content",
				MediaType:    "image/png",
				ByteSize:     123,
			}, nil
		},
	}
	added := providerStreamEvent{
		Type: "response.output_item.added",
		Item: json.RawMessage(`{
			"id":"image_1",
			"type":"image_generation_call",
			"status":"generating"
		}`),
	}
	done := providerStreamEvent{
		Type: "response.output_item.done",
		Item: json.RawMessage(`{
			"id":"image_1",
			"type":"image_generation_call",
			"status":"generating",
			"result":"final-image-base64"
		}`),
	}
	if err := accumulator.handle(stream, added); err != nil {
		t.Fatal(err)
	}
	if err := accumulator.handle(stream, done); err != nil {
		t.Fatal(err)
	}

	if len(accumulator.tools) != 1 || accumulator.tools[0].Status != "completed" {
		t.Fatalf("tools = %#v", accumulator.tools)
	}
	if len(accumulator.images) != 1 || accumulator.images[0].AttachmentID != "attachment_1" {
		t.Fatalf("images = %#v", accumulator.images)
	}
	parts := accumulator.parts()
	types := make([]string, len(parts))
	for index, part := range parts {
		types[index] = part.Type
	}
	if want := []string{"tool", "image"}; !slices.Equal(types, want) {
		t.Fatalf("part types = %v, want %v", types, want)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event: response.image") ||
		!strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("stream body = %s", body)
	}
}

func TestResponsePartsPreserveStreamOrderAndReasoningDuration(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{}
	events := []providerStreamEvent{
		{Type: "response.reasoning_summary_text.delta", Delta: "Checked the request."},
		{
			Type: "response.output_item.added",
			Item: json.RawMessage(`{
				"id":"search_1","type":"web_search_call","status":"in_progress",
				"action":{"type":"search","query":"example"}
			}`),
		},
		{Type: "response.output_text.delta", Delta: "Answer."},
		{
			Type: "response.output_item.done",
			Item: json.RawMessage(`{
				"id":"message_1","type":"message","status":"completed",
				"content":[{"type":"output_text","text":"Answer.","annotations":[{
					"type":"url_citation","url":"https://example.com","title":"Example"
				}]}]
			}`),
		},
	}
	for index, event := range events {
		if index == 1 {
			time.Sleep(2 * time.Millisecond)
		}
		if err := accumulator.handle(stream, event); err != nil {
			t.Fatal(err)
		}
	}

	parts := accumulator.parts()
	types := make([]string, len(parts))
	for index, part := range parts {
		types[index] = part.Type
	}
	if want := []string{"reasoning", "tool", "text", "citations"}; !slices.Equal(types, want) {
		t.Fatalf("part types = %v, want %v", types, want)
	}
	var reasoningData struct {
		DurationMS int64 `json:"durationMs"`
	}
	if err := json.Unmarshal(parts[0].JSONContent, &reasoningData); err != nil {
		t.Fatal(err)
	}
	if reasoningData.DurationMS < 1 {
		t.Fatalf("reasoning duration = %d", reasoningData.DurationMS)
	}
	var tool toolSnapshot
	if err := json.Unmarshal(parts[1].JSONContent, &tool); err != nil {
		t.Fatal(err)
	}
	if tool.Status != "in_progress" || tool.CallID != "search_1" {
		t.Fatalf("interrupted tool snapshot = %#v", tool)
	}
}

func TestSSEHeartbeatWritesComment(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	stream.start()
	ctx, cancel := context.WithCancel(context.Background())
	stop := stream.startHeartbeat(ctx, 2*time.Millisecond, cancel)
	time.Sleep(8 * time.Millisecond)
	stop()

	if body := recorder.Body.String(); !strings.Contains(body, ": keepalive\n\n") {
		t.Fatalf("heartbeat stream = %q", body)
	}
}

func TestConfigureImageGenerationRequestRequiresOnlyImageTool(t *testing.T) {
	request := provider.ResponsesRequest{
		Tools: []map[string]any{
			{"type": "web_search"},
			{"type": "image_generation", "quality": "low"},
		},
		ToolChoice: "auto",
	}
	configureImageGenerationRequest(&request, true)

	if request.ToolChoice != "required" || len(request.Tools) != 1 ||
		request.Tools[0]["type"] != "image_generation" {
		t.Fatalf("image request = %#v", request)
	}
	if _, exists := request.Tools[0]["quality"]; exists {
		t.Fatalf("image request overrides quality: %#v", request.Tools[0])
	}
}

func TestExplicitImageMarkerControlsRegenerationMode(t *testing.T) {
	accumulator := responseAccumulator{
		tools: []toolSnapshot{
			{CallID: "search-1", Type: "web_search", Status: "completed"},
			{CallID: "image-1", Type: "image_generation", Status: "completed"},
		},
		partOrder: []accumulatorPart{
			{Type: "tool", Key: "search-1"},
			{Type: "tool", Key: "image-1"},
		},
	}
	accumulator.markExplicitImageGeneration()
	parts := accumulator.parts()
	message := store.Message{Parts: []store.MessagePart{
		{Type: "tool", JSONContent: parts[0].JSONContent},
		{Type: "tool", JSONContent: parts[1].JSONContent},
		{Type: "image", AttachmentID: "generated-1"},
	}}
	if !messageRequestedImageGeneration(message) {
		t.Fatal("explicit image request marker was not detected")
	}
	message.Parts[1].JSONContent = json.RawMessage(
		`{"callId":"image-1","type":"image_generation","status":"completed"}`,
	)
	if messageRequestedImageGeneration(message) {
		t.Fatal("ordinary image output was treated as explicit image mode")
	}
}
