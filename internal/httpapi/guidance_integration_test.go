package httpapi

import (
	"context"
	"encoding/json"
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
		len(source.ProviderItems) != 0 {
		t.Fatalf("persisted clarification source = %#v", source)
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

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
