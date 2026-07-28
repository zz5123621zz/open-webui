package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestRestaurantGuidanceStructuredSubmissionHTTPFlow(t *testing.T) {
	var providerMu sync.Mutex
	providerRequests := make([]map[string]any, 0, 2)
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeGuidanceTestModel(w)
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			providerRequests = append(providerRequests, request)
			attempt := len(providerRequests)
			providerMu.Unlock()
			if attempt == 1 {
				writeGuidanceFunctionStream(
					w,
					"resp_guidance_cards",
					guidance.ToolShowClarificationCards,
					`{
						"schemaVersion":1,
						"intro":"先确认两个关键点。",
						"currentUnderstanding":["需要设计餐厅会员体系"],
						"questions":[{
							"key":"goal",
							"prompt":"首要目标是什么？",
							"selection":"single_select",
							"options":[
								{"key":"repeat","label":"增加复购","description":null},
								{"key":"cash","label":"回笼资金","description":null}
							],
							"allowOther":true,
							"allowDelegatedDefault":true,
							"minimumSelections":1,
							"maximumSelections":1
						},{
							"key":"audience",
							"prompt":"主要顾客是谁？",
							"selection":"single_select",
							"options":[
								{"key":"family","label":"家庭聚餐","description":null},
								{"key":"business","label":"商务宴请","description":null}
							],
							"allowOther":true,
							"allowDelegatedDefault":true,
							"minimumSelections":1,
							"maximumSelections":1
						}]
					}`,
				)
				return
			}
			writeCompletedTextStream(w, "resp_guidance_final", "这是按已确认需求生成的会员方案。")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mockProvider.Close)

	server, dataStore, user, cookie, csrf := startGuidanceIntegrationApp(
		t,
		mockProvider.URL,
	)
	conversation := createTestConversation(
		t,
		server.URL,
		cookie,
		csrf,
		"gpt-guidance",
		"high",
	)
	first := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie,
		csrf,
		`{
			"text":"帮我设计饭店的充卡和会员体系",
			"attachmentIds":[],
			"requestId":"guidance-http-1"
		}`,
	)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK ||
		!strings.Contains(string(firstBody), "event: response.completed") {
		t.Fatalf("initial guidance response status=%d body=%s", first.StatusCode, firstBody)
	}
	messages, err := dataStore.ListMessages(context.Background(), user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("initial guidance messages = %#v", messages)
	}
	source := messages[1]
	if source.Status != "completed" ||
		len(source.Parts) != 1 ||
		source.Parts[0].Type != guidance.PartClarification ||
		source.Parts[0].ID == "" ||
		len(source.ProviderItems) != 0 {
		t.Fatalf("persisted clarification source = %#v", source)
	}
	sourceCards, err := guidance.DecodeClarificationCards(source.Parts[0].JSONContent)
	if err != nil {
		t.Fatal(err)
	}
	if sourceCards.Round != 1 ||
		sourceCards.MaxRounds != guidance.MaximumClarificationRounds {
		t.Fatalf("initial clarification round metadata = %#v", sourceCards)
	}
	if !strings.Contains(
		string(firstBody),
		`"id":"`+source.Parts[0].ID+`"`,
	) {
		t.Fatalf(
			"completed clarification stream omitted persisted part id %q: %s",
			source.Parts[0].ID,
			firstBody,
		)
	}

	submissionBody, err := json.Marshal(map[string]any{
		"requestId": "guidance-http-2",
		"guidanceSubmission": map[string]any{
			"sourceAssistantMessageId": source.ID,
			"sourcePartId":             source.Parts[0].ID,
			"intent":                   guidance.IntentGenerateFromCurrent,
			"answers": []map[string]any{
				{
					"questionKey":        "goal",
					"selectedOptionKeys": []string{"repeat"},
				},
				{
					"questionKey":        "audience",
					"selectedOptionKeys": []string{"family"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie,
		csrf,
		string(submissionBody),
	)
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusOK ||
		!strings.Contains(string(secondBody), "event: response.completed") {
		t.Fatalf("structured guidance response status=%d body=%s", second.StatusCode, secondBody)
	}
	messages, err = dataStore.ListMessages(context.Background(), user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 ||
		messages[2].Parts[0].Type != guidance.PartClarificationSubmission ||
		!strings.Contains(messages[2].Parts[0].TextContent, "增加复购") ||
		messages[3].Status != "completed" ||
		len(messages[3].Parts) != 1 ||
		messages[3].Parts[0].TextContent != "这是按已确认需求生成的会员方案。" {
		t.Fatalf("completed structured guidance transcript = %#v", messages)
	}

	providerMu.Lock()
	requests := append([]map[string]any(nil), providerRequests...)
	providerMu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider guidance requests = %d, want 2", len(requests))
	}
	if !providerRequestHasTool(requests[0], guidance.ToolShowClarificationCards) {
		t.Fatalf("initial provider request did not expose clarification tool: %#v", requests[0])
	}
	if providerRequestHasTool(requests[1], guidance.ToolShowClarificationCards) ||
		providerRequestHasTool(requests[1], guidance.ToolShowTaskBrief) {
		t.Fatalf("confirmed generation still exposed guidance tools: %#v", requests[1]["tools"])
	}
	secondRequestRaw, _ := json.Marshal(requests[1])
	if !strings.Contains(string(secondRequestRaw), "本轮需求补充") ||
		!strings.Contains(string(secondRequestRaw), "增加复购") ||
		!strings.Contains(
			stringValue(requests[1]["instructions"]),
			"Produce the complete answer now",
		) {
		t.Fatalf("confirmed provider request lost normalized guidance history: %s", secondRequestRaw)
	}
}

func TestRestaurantGuidanceContinueRefiningRequiresThreeBoundedRounds(t *testing.T) {
	var providerMu sync.Mutex
	providerRequests := make([]map[string]any, 0, 5)
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeGuidanceTestModel(w)
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			providerRequests = append(providerRequests, request)
			attempt := len(providerRequests)
			providerMu.Unlock()
			switch attempt {
			case 1, 2, 3:
				writeGuidanceFunctionStream(
					w,
					fmt.Sprintf("resp_guidance_round_%d", attempt),
					guidance.ToolShowClarificationCards,
					guidanceRoundArguments(attempt),
				)
			case 4:
				writeGuidanceFunctionStream(
					w,
					"resp_guidance_brief",
					guidance.ToolShowTaskBrief,
					validTaskBriefArguments(),
				)
			default:
				writeCompletedTextStream(
					w,
					"resp_guidance_three_round_final",
					"这是经过三轮需求澄清后生成的会员方案。",
				)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mockProvider.Close)

	server, dataStore, user, cookie, csrf := startGuidanceIntegrationApp(
		t,
		mockProvider.URL,
	)
	conversation := createTestConversation(
		t,
		server.URL,
		cookie,
		csrf,
		"gpt-guidance",
		"high",
	)
	first := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie,
		csrf,
		`{
			"text":"帮我设计饭店的充卡和会员体系",
			"attachmentIds":[],
			"requestId":"guidance-three-round-1"
		}`,
	)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK ||
		!strings.Contains(string(firstBody), "event: response.completed") {
		t.Fatalf("initial three-round response status=%d body=%s", first.StatusCode, firstBody)
	}

	messages, err := dataStore.ListMessages(context.Background(), user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := messages[len(messages)-1]
	for round := 1; round <= guidance.MaximumClarificationRounds; round++ {
		if len(source.Parts) != 1 ||
			source.Parts[0].Type != guidance.PartClarification {
			t.Fatalf("round %d source = %#v", round, source)
		}
		cards, err := guidance.DecodeClarificationCards(source.Parts[0].JSONContent)
		if err != nil {
			t.Fatal(err)
		}
		if cards.Round != round ||
			cards.MaxRounds != guidance.MaximumClarificationRounds {
			t.Fatalf("round %d metadata = %#v", round, cards)
		}
		answers := make([]map[string]any, 0, len(cards.Questions))
		for _, question := range cards.Questions {
			answers = append(answers, map[string]any{
				"questionKey":        question.Key,
				"selectedOptionKeys": []string{question.Options[0].Key},
			})
		}
		submissionBody, err := json.Marshal(map[string]any{
			"requestId": fmt.Sprintf("guidance-three-round-%d", round+1),
			"guidanceSubmission": map[string]any{
				"sourceAssistantMessageId": source.ID,
				"sourcePartId":             source.Parts[0].ID,
				"intent":                   guidance.IntentContinueRefining,
				"answers":                  answers,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		response := authenticatedRequest(
			t,
			http.MethodPost,
			server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
			cookie,
			csrf,
			string(submissionBody),
		)
		responseBody, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK ||
			!strings.Contains(string(responseBody), "event: response.completed") {
			t.Fatalf(
				"round %d continuation status=%d body=%s",
				round,
				response.StatusCode,
				responseBody,
			)
		}
		messages, err = dataStore.ListMessages(
			context.Background(),
			user.ID,
			conversation.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		source = messages[len(messages)-1]
	}

	if len(source.Parts) != 1 ||
		source.Parts[0].Type != guidance.PartTaskBrief {
		t.Fatalf("three-round flow did not end in a task brief: %#v", source)
	}
	confirmBody, err := json.Marshal(map[string]any{
		"requestId": "guidance-three-round-final",
		"guidanceSubmission": map[string]any{
			"sourceAssistantMessageId": source.ID,
			"sourcePartId":             source.Parts[0].ID,
			"intent":                   guidance.IntentConfirmBrief,
			"answers":                  []any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	final := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie,
		csrf,
		string(confirmBody),
	)
	finalBody, _ := io.ReadAll(final.Body)
	final.Body.Close()
	if final.StatusCode != http.StatusOK ||
		!strings.Contains(
			string(finalBody),
			"这是经过三轮需求澄清后生成的会员方案。",
		) {
		t.Fatalf("three-round final status=%d body=%s", final.StatusCode, finalBody)
	}

	providerMu.Lock()
	requests := append([]map[string]any(nil), providerRequests...)
	providerMu.Unlock()
	if len(requests) != 5 {
		t.Fatalf("three-round provider requests = %d, want 5", len(requests))
	}
	for index, expectedRound := range []int{2, 3} {
		request := requests[index+1]
		if stringValue(request["tool_choice"]) != "required" ||
			!providerRequestHasOnlyTool(
				request,
				guidance.ToolShowClarificationCards,
			) ||
			!strings.Contains(
				stringValue(request["instructions"]),
				fmt.Sprintf("round %d of 3", expectedRound),
			) {
			t.Fatalf("required round %d request = %#v", expectedRound, request)
		}
	}
	if stringValue(requests[3]["tool_choice"]) != "required" ||
		!providerRequestHasOnlyTool(requests[3], guidance.ToolShowTaskBrief) ||
		!strings.Contains(
			stringValue(requests[3]["instructions"]),
			"limit of 3 rounds",
		) {
		t.Fatalf("post-round-limit task brief request = %#v", requests[3])
	}
	if providerRequestHasTool(requests[4], guidance.ToolShowClarificationCards) ||
		providerRequestHasTool(requests[4], guidance.ToolShowTaskBrief) {
		t.Fatalf("confirmed brief still exposed guidance tools: %#v", requests[4])
	}
}

func TestInvalidGuidanceOutputCanBeBypassedThroughHTTP(t *testing.T) {
	var providerMu sync.Mutex
	providerRequests := make([]map[string]any, 0, 2)
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeGuidanceTestModel(w)
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			providerRequests = append(providerRequests, request)
			attempt := len(providerRequests)
			providerMu.Unlock()
			if attempt == 1 {
				writeGuidanceFunctionStream(
					w,
					"resp_invalid_guidance",
					"invent_custom_restaurant_action",
					`{}`,
				)
				return
			}
			writeCompletedTextStream(w, "resp_guidance_bypass", "已按原问题直接回答。")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mockProvider.Close)

	server, dataStore, user, cookie, csrf := startGuidanceIntegrationApp(
		t,
		mockProvider.URL,
	)
	conversation := createTestConversation(
		t,
		server.URL,
		cookie,
		csrf,
		"gpt-guidance",
		"high",
	)
	first := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie,
		csrf,
		`{
			"text":"帮我设计十款特色煨汤",
			"attachmentIds":[],
			"requestId":"guidance-invalid-1"
		}`,
	)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK ||
		!strings.Contains(string(firstBody), `"code":"invalid_guidance_output"`) {
		t.Fatalf("invalid guidance status=%d body=%s", first.StatusCode, firstBody)
	}
	messages, err := dataStore.ListMessages(context.Background(), user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	invalid := messages[len(messages)-1]
	if invalid.Status != "error" ||
		invalid.ErrorCode != "invalid_guidance_output" ||
		len(invalid.Parts) != 1 ||
		invalid.Parts[0].Type != guidance.PartGuidanceError ||
		strings.Contains(invalid.Parts[0].TextContent, "invent_custom_restaurant_action") {
		t.Fatalf("persisted invalid guidance response = %#v", invalid)
	}

	bypassBody, err := json.Marshal(map[string]any{
		"requestId":      "guidance-invalid-2",
		"bypassGuidance": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bypass := authenticatedRequest(
		t,
		http.MethodPost,
		server.URL+"/api/v1/messages/"+invalid.ID+"/regenerate",
		cookie,
		csrf,
		string(bypassBody),
	)
	bypassResponseBody, _ := io.ReadAll(bypass.Body)
	bypass.Body.Close()
	if bypass.StatusCode != http.StatusOK ||
		!strings.Contains(string(bypassResponseBody), "event: response.completed") {
		t.Fatalf(
			"guidance bypass status=%d body=%s",
			bypass.StatusCode,
			bypassResponseBody,
		)
	}
	messages, err = dataStore.ListMessages(context.Background(), user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	final := messages[len(messages)-1]
	if final.Status != "completed" ||
		len(final.Parts) != 1 ||
		final.Parts[0].TextContent != "已按原问题直接回答。" {
		t.Fatalf("bypassed guidance response = %#v", final)
	}

	providerMu.Lock()
	requests := append([]map[string]any(nil), providerRequests...)
	providerMu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider bypass requests = %d, want 2", len(requests))
	}
	if providerRequestHasTool(requests[1], guidance.ToolShowClarificationCards) ||
		providerRequestHasTool(requests[1], guidance.ToolShowTaskBrief) ||
		!strings.Contains(
			stringValue(requests[1]["instructions"]),
			"Produce the complete answer now",
		) {
		t.Fatalf("bypass request did not force a normal final answer: %#v", requests[1])
	}
}

func startGuidanceIntegrationApp(
	t *testing.T,
	providerBaseURL string,
) (*httptest.Server, *store.Store, store.User, *http.Cookie, string) {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	dataStore, err := store.Open(ctx, filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	passwordHash, err := auth.HashPassword("guidance-test-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := dataStore.CreateUser(
		ctx,
		"guidance-http-user",
		"Guidance HTTP User",
		passwordHash,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx,
		user.Username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}
	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse(providerBaseURL + "/v1")
	cfg := config.Config{
		Environment:       "test",
		HTTPAddr:          ":0",
		BaseURL:           baseURL,
		DataDir:           dataDir,
		DatabasePath:      filepath.Join(dataDir, "app.db"),
		AppSecret:         []byte("01234567890123456789012345678901"),
		SessionTTL:        time.Hour,
		SessionCookieName: "owui_session",
		Provider: config.Provider{
			Kind:                      "cpa",
			BaseURL:                   providerURL,
			APIKey:                    "provider-test-key",
			DefaultModel:              "gpt-guidance",
			ModelsTimeout:             time.Second,
			DefaultReasoningEffort:    "high",
			UnknownModelContextTokens: 128000,
			RequestBodyMaxBytes:       50 << 20,
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal:  2,
			MaxConcurrentPerUser: 1,
			MaxQueuedPerUser:     1,
			QueueTimeout:         time.Second,
		},
		Tools: config.Tools{RestaurantGuidanceEnabled: true},
	}
	app := New(
		cfg,
		dataStore,
		provider.NewClient(cfg.Provider, "test"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	cookie, csrf := loginTestUser(
		t,
		server.URL,
		user.Username,
		"guidance-test-password",
	)
	return server, dataStore, user, cookie, csrf
}

func writeGuidanceTestModel(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"models":[{
		"slug":"gpt-guidance",
		"display_name":"GPT Guidance",
		"context_window":128000,
		"input_modalities":["text"],
		"supported_reasoning_levels":[{"effort":"high"}],
		"default_reasoning_level":"high",
		"priority":1
	}]}`)
}

func writeGuidanceFunctionStream(
	w http.ResponseWriter,
	responseID string,
	name string,
	arguments string,
) {
	w.Header().Set("Content-Type", "text/event-stream")
	item, _ := json.Marshal(map[string]any{
		"id":        "call_" + responseID,
		"type":      "function_call",
		"status":    "completed",
		"name":      name,
		"arguments": arguments,
	})
	done, _ := json.Marshal(map[string]any{
		"type": "response.output_item.done",
		"item": json.RawMessage(item),
	})
	completed, _ := json.Marshal(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": responseID, "status": "completed",
		},
	})
	_, _ = io.WriteString(w, "data: "+string(done)+"\n\n")
	_, _ = io.WriteString(w, "data: "+string(completed)+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func guidanceRoundArguments(round int) string {
	return fmt.Sprintf(`{
		"schemaVersion":1,
		"intro":"继续确认本轮最关键的两个问题。",
		"currentUnderstanding":["正在逐轮完善餐厅会员体系"],
		"questions":[{
			"key":"round_%d_goal",
			"prompt":"第%d轮的首要选择是什么？",
			"selection":"single_select",
			"options":[
				{"key":"practical","label":"优先落地","description":null},
				{"key":"distinctive","label":"优先特色","description":null}
			],
			"allowOther":true,
			"allowDelegatedDefault":true,
			"minimumSelections":1,
			"maximumSelections":1
		},{
			"key":"round_%d_scope",
			"prompt":"第%d轮希望覆盖哪种范围？",
			"selection":"single_select",
			"options":[
				{"key":"focused","label":"先做核心部分","description":null},
				{"key":"complete","label":"给出完整方案","description":null}
			],
			"allowOther":true,
			"allowDelegatedDefault":true,
			"minimumSelections":1,
			"maximumSelections":1
		}]
	}`, round, round, round, round)
}

func providerRequestHasTool(request map[string]any, name string) bool {
	tools, _ := request["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if stringValue(tool["name"]) == name {
			return true
		}
	}
	return false
}

func providerRequestHasOnlyTool(request map[string]any, name string) bool {
	tools, _ := request["tools"].([]any)
	return len(tools) == 1 && providerRequestHasTool(request, name)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
