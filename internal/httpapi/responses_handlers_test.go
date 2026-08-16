package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestSafeWebActionUsesStrictAllowlist(t *testing.T) {
	raw := safeWebAction(map[string]any{
		"type":          "search",
		"query":         "safe query",
		"url":           "https://user:password@example.com/result?page=2&utm_source=test&token=secret#private",
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
	if got["url"] != "https://example.com/result?page=2" {
		t.Fatalf("sanitized action URL = %q", got["url"])
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
	if got := sanitizeCitationURL(
		"https://example.com/path?page=2&utm_campaign=x&fbclid=track&access_token=secret#section",
	); got != "https://example.com/path?page=2" {
		t.Fatalf("sanitized URL query = %q", got)
	}
	if got := sanitizeCitationURL(
		"https://example.com/object?page=2&X-Amz-Credential=credential&X-Amz-Signature=secret",
	); got != "https://example.com/object?page=2" {
		t.Fatalf("sanitized signed URL query = %q", got)
	}
	if got := sanitizeCitationURL("https://[::1"); got != "" {
		t.Fatalf("malformed URL = %q", got)
	}
}

func TestProviderReplayItemIsSanitizedBeforePersistence(t *testing.T) {
	accumulator := &responseAccumulator{}
	accumulator.captureProviderItem(json.RawMessage(`{
		"id":"message_1",
		"type":"message",
		"authorization":"Bearer provider-secret",
		"headers":{"Cookie":"session=provider-secret"},
		"encrypted_content":"opaque-private-reasoning",
		"content":[{
			"type":"output_text",
			"text":"Safe answer",
			"annotations":[{
				"type":"url_citation",
				"url":"https://user:pass@example.com/page?page=2&utm_source=test&token=secret#private"
			}]
		}]
	}`))
	if len(accumulator.providerItems) != 1 {
		t.Fatalf("provider items = %#v", accumulator.providerItems)
	}
	raw := string(accumulator.providerItems[0].ReplayJSON)
	for _, forbidden := range []string{
		"provider-secret", "opaque-private-reasoning", "user", "pass",
		"utm_source", "token", "#private",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("provider replay item contains %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(raw, `"url":"https://example.com/page?page=2"`) {
		t.Fatalf("provider replay URL was not safely preserved: %s", raw)
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
		{
			Type: "response.reasoning_summary_text.delta", ItemID: "reasoning_1",
			OutputIndex: 0, SummaryIndex: 0, Delta: "Checked the request.",
		},
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
		DurationMS     int64  `json:"durationMs"`
		ProviderItemID string `json:"providerItemId"`
		SummaryIndex   int    `json:"summaryIndex"`
	}
	if err := json.Unmarshal(parts[0].JSONContent, &reasoningData); err != nil {
		t.Fatal(err)
	}
	if reasoningData.DurationMS < 1 {
		t.Fatalf("reasoning duration = %d", reasoningData.DurationMS)
	}
	if reasoningData.ProviderItemID != "reasoning_1" || reasoningData.SummaryIndex != 0 {
		t.Fatalf("reasoning identity = %#v", reasoningData)
	}
	var tool toolSnapshot
	if err := json.Unmarshal(parts[1].JSONContent, &tool); err != nil {
		t.Fatal(err)
	}
	if tool.Status != "in_progress" || tool.CallID != "search_1" {
		t.Fatalf("interrupted tool snapshot = %#v", tool)
	}
}

func TestReasoningSectionsPreserveProviderIdentityAndDeduplicateCompletedItem(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{}
	events := []providerStreamEvent{
		{
			Type: "response.reasoning_summary_text.delta", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 0, Delta: "第一段",
		},
		{
			Type: "response.reasoning_summary_text.done", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 0, Text: "第一段完成",
		},
		{
			Type: "response.reasoning_summary_text.done", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 0, Text: "第一段完成",
		},
		{
			Type: "response.reasoning_summary_text.done", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 9,
		},
		{
			Type: "response.reasoning_summary_text.delta", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 1, Delta: "第二段",
		},
		{
			Type: "response.reasoning_summary_text.done", ItemID: "reasoning_1",
			OutputIndex: 2, SummaryIndex: 1, Text: "第二段完成",
		},
		{
			Type: "response.output_item.done", OutputIndex: 2,
			Item: json.RawMessage(`{
				"id":"reasoning_1","type":"reasoning","status":"completed",
				"encrypted_content":"must-not-be-saved",
				"summary":[
					{"type":"summary_text","text":"第一段完成"},
					{"type":"summary_text","text":"第二段完成"}
				]
			}`),
		},
	}
	for _, event := range events {
		if err := accumulator.handle(stream, event); err != nil {
			t.Fatal(err)
		}
	}

	parts := accumulator.parts()
	if len(parts) != 2 || parts[0].Type != "reasoning" || parts[1].Type != "reasoning" {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[0].TextContent != "第一段完成" || parts[1].TextContent != "第二段完成" {
		t.Fatalf("reasoning texts = %q, %q", parts[0].TextContent, parts[1].TextContent)
	}
	for index, part := range parts {
		var metadata struct {
			ProviderItemID string `json:"providerItemId"`
			SummaryIndex   int    `json:"summaryIndex"`
			OutputIndex    int    `json:"outputIndex"`
			Completed      bool   `json:"completed"`
		}
		if err := json.Unmarshal(part.JSONContent, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.ProviderItemID != "reasoning_1" ||
			metadata.SummaryIndex != index || metadata.OutputIndex != 2 ||
			!metadata.Completed {
			t.Fatalf("metadata[%d] = %#v", index, metadata)
		}
	}
	if len(accumulator.providerItems) != 0 {
		t.Fatalf("reasoning provider item was captured: %#v", accumulator.providerItems)
	}
	body := recorder.Body.String()
	if strings.Count(body, "event: response.reasoning.done") != 2 {
		t.Fatalf("completed section events were duplicated:\n%s", body)
	}
	if strings.Contains(body, "encrypted_content") ||
		strings.Contains(string(parts[0].JSONContent), "encrypted_content") {
		t.Fatalf("encrypted reasoning leaked:\n%s", body)
	}
}

func TestProviderStreamFailurePreservesTopLevelCodeAndFailureUsage(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{}
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.failed",
		Code: "internal_server_error",
		Response: json.RawMessage(`{
			"id":"resp_failed",
			"error":{"type":"server_error"},
			"usage":{
				"input_tokens":120,
				"output_tokens":45,
				"output_tokens_details":{"reasoning_tokens":20}
			}
		}`),
	}); err != nil {
		t.Fatal(err)
	}
	if accumulator.failureCode != "server_error" ||
		accumulator.responseID != "resp_failed" ||
		accumulator.inputTokens != 120 ||
		accumulator.outputTokens != 45 ||
		accumulator.reasoningTokens != 20 {
		t.Fatalf("failed response metadata = %#v", accumulator)
	}

	topLevelOnly := &responseAccumulator{}
	if err := topLevelOnly.handle(stream, providerStreamEvent{
		Type: "error",
		Code: "internal_server_error",
	}); err != nil {
		t.Fatal(err)
	}
	if topLevelOnly.failureCode != "internal_server_error" {
		t.Fatalf("top-level stream failure = %q", topLevelOnly.failureCode)
	}

	incomplete := &responseAccumulator{}
	if err := incomplete.handle(stream, providerStreamEvent{
		Type: "response.incomplete",
		Response: json.RawMessage(`{
			"id":"resp_incomplete",
			"incomplete_details":{"reason":"max_output_tokens"},
			"usage":{"input_tokens":80,"output_tokens":50}
		}`),
	}); err != nil {
		t.Fatal(err)
	}
	if incomplete.failureCode != "max_output_tokens" ||
		incomplete.responseID != "resp_incomplete" ||
		incomplete.inputTokens != 80 ||
		incomplete.outputTokens != 50 {
		t.Fatalf("incomplete response metadata = %#v", incomplete)
	}
}

func TestProviderContinuationIsLimitedToSafeRestaurantFinalAnswers(t *testing.T) {
	accumulator := &responseAccumulator{
		failureCode: "internal_server_error",
		tools: []toolSnapshot{{
			Type: "web_search", Status: "completed",
		}},
	}
	accumulator.text.WriteString("已经生成的前半部分")
	finalGuidance := guidance.Runtime{Enabled: true, FinalAnswer: true}
	if !canSafelyContinueProviderResponse(
		accumulator,
		finalGuidance,
		false,
	) {
		t.Fatal("safe transient restaurant final answer was not eligible for continuation")
	}

	accumulator.completed = true
	if canSafelyContinueProviderResponse(accumulator, finalGuidance, false) {
		t.Fatal("completed response was eligible for automatic continuation")
	}
	accumulator.completed = false
	accumulator.tools = append(accumulator.tools, toolSnapshot{
		Type: "image_generation", Status: "completed",
	})
	if canSafelyContinueProviderResponse(accumulator, finalGuidance, false) {
		t.Fatal("image-producing response was eligible for automatic continuation")
	}
	accumulator.tools = accumulator.tools[:1]
	if canSafelyContinueProviderResponse(
		accumulator,
		guidance.Runtime{Enabled: true},
		false,
	) {
		t.Fatal("unconfirmed response was eligible for automatic continuation")
	}
	accumulator.failureCode = "rate_limit_exceeded"
	if canSafelyContinueProviderResponse(accumulator, finalGuidance, false) {
		t.Fatal("non-transient provider failure was eligible for automatic continuation")
	}
}

func TestProviderContinuationRequestUsesPartialTextWithoutTools(t *testing.T) {
	original := provider.ResponsesRequest{
		Instructions: "Original instructions.",
		Input: []provider.ResponseInput{{
			Role: "user", Content: "请完成方案",
		}},
		Tools: []map[string]any{
			{"type": "web_search"},
			{"type": "image_generation"},
		},
		ToolChoice: "auto",
	}
	continuation := providerContinuationRequest(original, "已完成的前半部分")
	if len(original.Input) != 1 ||
		len(original.Tools) != 2 ||
		original.ToolChoice != "auto" {
		t.Fatalf("original request was mutated: %#v", original)
	}
	if len(continuation.Input) != 3 ||
		len(continuation.Tools) != 0 ||
		continuation.ToolChoice != "" ||
		!strings.Contains(
			continuation.Instructions,
			"Continue only the missing remainder",
		) {
		t.Fatalf("continuation request = %#v", continuation)
	}
	partial, ok := continuation.Input[1].Content.([]provider.ResponseContent)
	if !ok ||
		len(partial) != 1 ||
		partial[0].Type != "output_text" ||
		partial[0].Text != "已完成的前半部分" ||
		continuation.Input[1].Role != "assistant" ||
		continuation.Input[2].Role != "developer" {
		t.Fatalf("continuation input = %#v", continuation.Input)
	}
}

func TestProviderContinuationAccumulatorKeepsCombinedTextWithoutReplayItems(
	t *testing.T,
) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := &responseAccumulator{
		suppressProviderItems: true,
	}
	accumulator.text.WriteString("已完成的前半部分；")
	accumulator.responseTextStart = accumulator.text.Len()
	item := json.RawMessage(`{
		"id":"continuation_message",
		"type":"message",
		"status":"completed",
		"content":[{"type":"output_text","text":"从断点继续的后半部分。"}]
	}`)
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.output_item.done",
		Item: item,
	}); err != nil {
		t.Fatal(err)
	}
	if accumulator.text.String() !=
		"已完成的前半部分；从断点继续的后半部分。" {
		t.Fatalf("combined continuation text = %q", accumulator.text.String())
	}
	if len(accumulator.providerItems) != 0 {
		t.Fatalf(
			"continuation provider replay items = %#v",
			accumulator.providerItems,
		)
	}
}

func TestProgressiveSummaryUnsupportedMatcherIsExact(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "known unsupported field",
			err: &provider.HTTPError{
				StatusCode: http.StatusBadRequest,
				Code:       "invalid_parameter",
				Message:    "Unknown parameter: stream_options.reasoning_summary_delivery",
			},
			want: true,
		},
		{
			name: "wrong status",
			err: &provider.HTTPError{
				StatusCode: http.StatusUnprocessableEntity,
				Code:       "invalid_parameter",
				Message:    "Unknown parameter: stream_options.reasoning_summary_delivery",
			},
		},
		{
			name: "field mentioned without unsupported marker",
			err: &provider.HTTPError{
				StatusCode: http.StatusBadRequest,
				Code:       "invalid_request",
				Message:    "stream_options could not be processed",
			},
		},
		{
			name: "unrelated unknown field",
			err: &provider.HTTPError{
				StatusCode: http.StatusBadRequest,
				Code:       "invalid_parameter",
				Message:    "Unknown parameter: temperature",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isProgressiveSummaryUnsupported(test.err); got != test.want {
				t.Fatalf("isProgressiveSummaryUnsupported() = %v, want %v", got, test.want)
			}
		})
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

func TestGuidanceFunctionCallBecomesValidatedStructuredPart(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := responseAccumulator{
		guidanceRuntime: guidance.Runtime{
			Enabled: true, AllowClarification: true, AllowTaskBrief: true,
			MaxQuestions: 2,
		},
		guidanceInstanceID: "assistant_guidance_1",
	}
	arguments := `{
		"schemaVersion":1,
		"intro":"先确认两个关键点。",
		"currentUnderstanding":["需要设计会员体系"],
		"questions":[{
			"key":"goal","prompt":"首要目标是什么？","selection":"single_select",
			"options":[
				{"key":"repeat","label":"增加复购","description":null},
				{"key":"cash","label":"回笼资金","description":null}
			],
			"allowOther":true,"allowDelegatedDefault":true,
			"minimumSelections":1,"maximumSelections":1
		},{
			"key":"audience","prompt":"主要顾客是谁？","selection":"single_select",
			"options":[
				{"key":"family","label":"家庭聚餐","description":null},
				{"key":"business","label":"商务宴请","description":null}
			],
			"allowOther":true,"allowDelegatedDefault":true,
			"minimumSelections":1,"maximumSelections":1
		}]
	}`
	item, err := json.Marshal(map[string]any{
		"id":        "call_1",
		"type":      "function_call",
		"status":    "completed",
		"name":      guidance.ToolShowClarificationCards,
		"arguments": arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := accumulator.handle(stream, providerStreamEvent{
		Type: "response.output_item.done", Item: item,
	}); err != nil {
		t.Fatal(err)
	}
	accumulator.finalizeGuidance()
	parts := accumulator.parts()
	if accumulator.failureCode != "" || len(parts) != 1 ||
		parts[0].Type != guidance.PartClarification ||
		!strings.Contains(parts[0].TextContent, "增加复购") {
		t.Fatalf(
			"validated guidance output = failure %q parts %#v",
			accumulator.failureCode, parts,
		)
	}
	cards, err := guidance.DecodeClarificationCards(parts[0].JSONContent)
	if err != nil {
		t.Fatal(err)
	}
	if cards.InstanceID != "assistant_guidance_1" ||
		cards.Round != 1 ||
		cards.MaxRounds != guidance.MaximumClarificationRounds ||
		len(cards.Questions) != 2 {
		t.Fatalf("stored guidance cards = %#v", cards)
	}
	if len(accumulator.providerItems) != 0 {
		t.Fatalf("function call was persisted as provider replay: %#v", accumulator.providerItems)
	}
}

func TestRequiredGuidanceRejectsPlainTextWithoutControlCall(t *testing.T) {
	accumulator := responseAccumulator{
		guidanceRuntime: guidance.Runtime{
			Enabled:              true,
			AllowClarification:   true,
			RequireClarification: true,
			MaxQuestions:         guidance.MaximumQuestionsPerRound,
			MaxRounds:            guidance.MaximumClarificationRounds,
			RoundCount:           1,
		},
	}
	accumulator.text.WriteString("模型错误地直接回答了任务。")
	accumulator.finalizeGuidance()
	parts := accumulator.parts()
	if accumulator.failureCode != "invalid_guidance_output" ||
		len(parts) != 1 ||
		parts[0].Type != guidance.PartGuidanceError ||
		strings.Contains(parts[0].TextContent, "模型错误") {
		t.Fatalf(
			"required guidance without control = failure %q parts %#v",
			accumulator.failureCode,
			parts,
		)
	}
}

func TestRequiredGuidancePreservesProviderFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream := newSSEWriter(recorder)
	accumulator := responseAccumulator{
		guidanceRuntime: guidance.Runtime{
			Enabled:              true,
			AllowClarification:   true,
			RequireClarification: true,
			MaxQuestions:         guidance.MaximumQuestionsPerRound,
			MaxRounds:            guidance.MaximumClarificationRounds,
			RoundCount:           1,
		},
	}
	event := providerStreamEvent{
		Type:  "error",
		Error: json.RawMessage(`{"type":"server_error","code":"internal_server_error"}`),
	}
	if err := accumulator.handle(stream, event); err != nil {
		t.Fatal(err)
	}
	accumulator.finalizeGuidance()
	parts := accumulator.parts()
	if accumulator.failureCode != "internal_server_error" ||
		len(parts) != 0 ||
		accumulator.guidanceControlPart != nil {
		t.Fatalf(
			"provider failure was overwritten = failure %q parts %#v control %#v",
			accumulator.failureCode,
			parts,
			accumulator.guidanceControlPart,
		)
	}
}

func TestInvalidGuidanceOutputPersistsOnlyFixedSafePart(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []providerStreamEvent
	}{
		{
			name: "unknown function",
			events: []providerStreamEvent{guidanceFunctionEvent(
				t, "call_1", "invent_custom_button", `{}`,
			)},
		},
		{
			name: "multiple functions",
			events: []providerStreamEvent{
				guidanceFunctionEvent(
					t, "call_1", guidance.ToolShowTaskBrief, validTaskBriefArguments(),
				),
				guidanceFunctionEvent(
					t, "call_2", guidance.ToolShowTaskBrief, validTaskBriefArguments(),
				),
			},
		},
		{
			name: "function and substantive text",
			events: []providerStreamEvent{
				{Type: "response.output_text.delta", Delta: "模型自行生成的正文"},
				guidanceFunctionEvent(
					t, "call_1", guidance.ToolShowTaskBrief, validTaskBriefArguments(),
				),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			stream := newSSEWriter(recorder)
			accumulator := responseAccumulator{
				guidanceRuntime: guidance.Runtime{
					Enabled: true, AllowClarification: true,
					AllowTaskBrief: true, MaxQuestions: 3,
				},
				guidanceInstanceID: "assistant_invalid_guidance",
				providerItems: []store.NewProviderItem{{
					ItemType: "message", ReplayJSON: json.RawMessage(`{"type":"message"}`),
				}},
			}
			for _, event := range test.events {
				if err := accumulator.handle(stream, event); err != nil {
					t.Fatal(err)
				}
			}
			accumulator.finalizeGuidance()
			parts := accumulator.parts()
			if accumulator.failureCode != "invalid_guidance_output" ||
				len(parts) != 1 ||
				parts[0].Type != guidance.PartGuidanceError ||
				strings.Contains(parts[0].TextContent, "模型自行生成") ||
				len(accumulator.providerItems) != 0 {
				t.Fatalf(
					"invalid guidance output = failure %q parts %#v provider %#v",
					accumulator.failureCode, parts, accumulator.providerItems,
				)
			}
		})
	}
}

func guidanceFunctionEvent(
	t *testing.T,
	id string,
	name string,
	arguments string,
) providerStreamEvent {
	t.Helper()
	item, err := json.Marshal(map[string]any{
		"id": id, "type": "function_call", "status": "completed",
		"name": name, "arguments": arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	return providerStreamEvent{Type: "response.output_item.done", Item: item}
}

func validTaskBriefArguments() string {
	return `{
		"schemaVersion":1,
		"goal":"设计会员体系",
		"context":[],
		"constraints":[],
		"desiredOutput":["充值档位和规则"],
		"delegatedAssumptions":[],
		"unresolved":[],
		"profileUpdateProposal":null
	}`
}
