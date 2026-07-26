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
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestEditSearchAndUsageEndpoints(t *testing.T) {
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{
				"slug":"gpt-chat","display_name":"GPT Chat","context_window":200000,
				"input_modalities":["text"],"supports_search_tool":false,
				"supported_reasoning_levels":[{"effort":"high"}],
				"default_reasoning_level":"high","priority":1
			}]}`)
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			events := []string{
				`{"type":"response.created","response":{"id":"resp_edit"}}`,
				`{"type":"response.output_text.delta","delta":"Edited answer."}`,
				`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","status":"completed","content":[{"type":"output_text","text":"Edited answer."}]}}`,
				`{"type":"response.completed","response":{"id":"resp_edit","status":"completed","usage":{"input_tokens":21,"output_tokens":8,"output_tokens_details":{"reasoning_tokens":2}}}}`,
			}
			for _, event := range events {
				_, _ = io.WriteString(w, "data: "+event+"\n\n")
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockProvider.Close()

	dataDir := t.TempDir()
	dataStore, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	passwordHash, _ := auth.HashPassword("correct horse battery")
	if _, err := dataStore.CreateUser(context.Background(), "alice", "Alice", passwordHash); err != nil {
		t.Fatal(err)
	}

	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse(mockProvider.URL + "/v1")
	cfg := config.Config{
		Environment: "test", HTTPAddr: ":0", BaseURL: baseURL, DataDir: dataDir,
		DatabasePath: filepath.Join(dataDir, "app.db"), AppSecret: []byte("01234567890123456789012345678901"),
		SessionTTL: time.Hour, SessionCookieName: "owui_session",
		Provider: config.Provider{
			Kind: "cpa", BaseURL: providerURL, APIKey: "provider-test-key",
			DefaultModel: "gpt-chat", ModelsTimeout: time.Second,
			DefaultReasoningEffort: "auto", UnknownModelContextTokens: 128000,
			RequestBodyMaxBytes: 50 << 20,
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal: 4, MaxConcurrentPerUser: 2, MaxQueuedPerUser: 2, QueueTimeout: time.Second,
		},
		Tools: config.Tools{WebSearchEnabled: false, ImageGenerationEnabled: false},
	}
	modelClient := provider.NewClient(cfg.Provider, "test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(cfg, dataStore, modelClient, logger)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	cookie, csrf := loginTestUser(t, server.URL, "alice", "correct horse battery")
	conversation := createTestConversation(t, server.URL, cookie, csrf, "gpt-chat", "high")

	first := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie, csrf, `{"text":"original question about pandas","requestId":"edit-flow-1"}`,
	)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK || !strings.Contains(string(firstBody), "response.completed") {
		t.Fatalf("first response status=%d body=%s", first.StatusCode, firstBody)
	}

	list := authenticatedRequest(
		t, http.MethodGet,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/messages", cookie, "", "",
	)
	var listed struct {
		Messages []store.Message `json:"messages"`
	}
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	list.Body.Close()
	if len(listed.Messages) != 2 || listed.Messages[0].Role != "user" {
		t.Fatalf("messages after first turn = %#v", listed.Messages)
	}
	userMessageID := listed.Messages[0].ID

	// Editing an assistant message is rejected before any job starts.
	rejected := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/messages/"+listed.Messages[1].ID+"/edit",
		cookie, csrf, `{"text":"rewritten","requestId":"edit-flow-2"}`,
	)
	rejectedBody, _ := io.ReadAll(rejected.Body)
	rejected.Body.Close()
	if rejected.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(rejectedBody), "message_not_editable") {
		t.Fatalf("assistant edit status=%d body=%s", rejected.StatusCode, rejectedBody)
	}

	edit := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/messages/"+userMessageID+"/edit",
		cookie, csrf, `{"text":"rewritten question about red pandas","requestId":"edit-flow-3"}`,
	)
	editBody, _ := io.ReadAll(edit.Body)
	edit.Body.Close()
	editText := string(editBody)
	if edit.StatusCode != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edit.StatusCode, editText)
	}
	for _, expected := range []string{
		"event: response.started", "rewritten question about red pandas",
		"event: response.completed",
	} {
		if !strings.Contains(editText, expected) {
			t.Fatalf("edit stream missing %q:\n%s", expected, editText)
		}
	}

	list = authenticatedRequest(
		t, http.MethodGet,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/messages", cookie, "", "",
	)
	if err := json.NewDecoder(list.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	list.Body.Close()
	if len(listed.Messages) != 3 {
		t.Fatalf("messages after edit = %#v", listed.Messages)
	}
	if got := listed.Messages[0].Parts[0].TextContent; got != "rewritten question about red pandas" {
		t.Fatalf("edited user text = %q", got)
	}
	tail := listed.Messages[2]
	if tail.Role != "assistant" || tail.Status != "completed" ||
		tail.ParentMessageID != userMessageID {
		t.Fatalf("edit assistant = %#v", tail)
	}

	// The search endpoint finds the conversation by the edited text.
	search := authenticatedRequest(
		t, http.MethodGet,
		server.URL+"/api/v1/search?q="+url.QueryEscape("red pandas"), cookie, "", "",
	)
	var searched struct {
		Results []store.ConversationSearchResult `json:"results"`
	}
	if err := json.NewDecoder(search.Body).Decode(&searched); err != nil {
		t.Fatal(err)
	}
	search.Body.Close()
	if search.StatusCode != http.StatusOK || len(searched.Results) != 1 ||
		searched.Results[0].Conversation.ID != conversation.ID {
		t.Fatalf("search status=%d results=%#v", search.StatusCode, searched.Results)
	}
	if searched.Results[0].MatchedIn != "message" ||
		!strings.Contains(searched.Results[0].Snippet, "red pandas") {
		t.Fatalf("search result = %#v", searched.Results[0])
	}

	// The usage endpoint aggregates both completed responses.
	usage := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/usage", cookie, "", "",
	)
	var usagePayload struct {
		Usage []store.UsageRow `json:"usage"`
	}
	if err := json.NewDecoder(usage.Body).Decode(&usagePayload); err != nil {
		t.Fatal(err)
	}
	usage.Body.Close()
	if usage.StatusCode != http.StatusOK || len(usagePayload.Usage) != 1 {
		t.Fatalf("usage status=%d rows=%#v", usage.StatusCode, usagePayload.Usage)
	}
	row := usagePayload.Usage[0]
	if row.Model != "gpt-chat" || row.Responses != 2 || row.InputTokens != 42 ||
		row.OutputTokens != 16 || row.ReasoningTokens != 4 {
		t.Fatalf("usage row = %#v", row)
	}
}
