package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/auth"
	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/progressivesummary"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestAuthenticatedChatFlow(t *testing.T) {
	var providerMu sync.Mutex
	var providerRequest map[string]any
	var dedicatedImageRequest map[string]any
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer provider-test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{
				"slug":"gpt-chat","display_name":"GPT Chat","context_window":200000,
				"input_modalities":["text","image"],"supports_search_tool":true,
				"supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}],
				"default_reasoning_level":"low","priority":1
			},{
				"slug":"grok-4.5","display_name":"Grok 4.5","context_window":131072,
				"input_modalities":["text","image"],"supports_search_tool":false,
				"supported_reasoning_levels":[{"effort":"high"}],
				"default_reasoning_level":"high","priority":2
			}]}`)
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			providerRequest = request
			providerMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			events := []string{
				`{"type":"response.created","response":{"id":"resp_test"}}`,
				`{"type":"response.reasoning_summary_text.delta","item_id":"reasoning_1","output_index":0,"summary_index":0,"delta":"Checked the request."}`,
				`{"type":"response.reasoning_summary_text.done","item_id":"reasoning_1","output_index":0,"summary_index":0,"text":"Checked the request."}`,
				`{"type":"response.reasoning_summary_text.delta","item_id":"reasoning_1","output_index":0,"summary_index":1,"delta":"Verified the source."}`,
				`{"type":"response.reasoning_summary_text.done","item_id":"reasoning_1","output_index":0,"summary_index":1,"text":"Verified the source."}`,
				`{"type":"response.output_item.added","item":{"id":"search_1","type":"web_search_call","status":"in_progress","action":{"type":"search","query":"example"}}}`,
				`{"type":"response.output_item.done","item":{"id":"search_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"example"}}}`,
				`{"type":"response.output_text.delta","delta":"Hello from CPA."}`,
				`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","status":"completed","content":[{"type":"output_text","text":"Hello from CPA.","annotations":[{"type":"url_citation","url":"https://example.com","title":"Example","start_index":0,"end_index":5}]}]}}`,
				`{"type":"response.output_item.done","item":{"id":"img_1","type":"image_generation_call","status":"completed","output_format":"png","result":"` + onePixelPNG + `"}}`,
				`{"type":"response.completed","response":{"id":"resp_test","status":"completed","usage":{"input_tokens":11,"output_tokens":7,"output_tokens_details":{"reasoning_tokens":3}}}}`,
			}
			for _, event := range events {
				_, _ = io.WriteString(w, "data: "+event+"\n\n")
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case "/v1/images/generations":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			dedicatedImageRequest = request
			providerMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"created":456,"data":[{"b64_json":"`+onePixelPNG+`"}]}`)
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
	user, err := dataStore.CreateUser(context.Background(), "alice", "Alice", passwordHash)
	if err != nil {
		t.Fatal(err)
	}
	other, err := dataStore.CreateUser(context.Background(), "bob", "Bob", passwordHash)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dataStore.CreateUserWithRole(
		context.Background(), "admin", "Administrator", passwordHash, "admin",
	)
	if err != nil {
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
			ResponseImageModels: []string{"gpt-chat"},
			DedicatedImageModels: map[string]string{
				"grok-4.5": "grok-imagine-image-quality",
			},
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal: 4, MaxConcurrentPerUser: 2, MaxQueuedPerUser: 2, QueueTimeout: time.Second,
		},
		Tools: config.Tools{WebSearchEnabled: true, ImageGenerationEnabled: true},
	}
	modelClient := provider.NewClient(cfg.Provider, "test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(cfg, dataStore, modelClient, logger)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	cookie, csrf := loginTestUser(t, server.URL, "alice", "correct horse battery")
	conversation := createTestConversation(t, server.URL, cookie, csrf, "gpt-chat", "high")

	requestBody := `{"text":"Hello","attachmentIds":[],"requestId":"request-1"}`
	response := authenticatedRequest(t, http.MethodPost, server.URL+"/api/v1/conversations/"+conversation.ID+"/responses", cookie, csrf, requestBody)
	streamBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("response status = %d body=%s", response.StatusCode, streamBody)
	}
	streamText := string(streamBody)
	for _, expected := range []string{
		"event: response.started", "event: response.reasoning.delta",
		"event: response.reasoning.done", "event: response.tool",
		"event: response.text.delta", "event: response.image", "event: response.completed",
	} {
		if !strings.Contains(streamText, expected) {
			t.Fatalf("stream missing %q:\n%s", expected, streamText)
		}
	}

	providerMu.Lock()
	gotRequest := providerRequest
	providerMu.Unlock()
	safetyIdentifier, _ := gotRequest["safety_identifier"].(string)
	if len(safetyIdentifier) < 32 || safetyIdentifier == user.ID ||
		strings.Contains(safetyIdentifier, user.Username) {
		t.Fatalf("provider safety_identifier = %q", safetyIdentifier)
	}
	instructions, _ := gotRequest["instructions"].(string)
	for _, expected := range []string{
		"China-first web-search strategy",
		"Simplified Chinese queries",
		"高德地图",
		"same-named foreign entity",
		"distinguish official facts from reviews",
	} {
		if !strings.Contains(instructions, expected) {
			t.Fatalf("provider instructions missing %q: %q", expected, instructions)
		}
	}
	reasoning, _ := gotRequest["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("provider reasoning = %#v", reasoning)
	}
	streamOptions, _ := gotRequest["stream_options"].(map[string]any)
	if streamOptions["reasoning_summary_delivery"] != "sequential_cutoff" {
		t.Fatalf("provider stream_options = %#v", streamOptions)
	}
	rawTools, _ := gotRequest["tools"].([]any)
	if len(rawTools) != 2 {
		t.Fatalf("provider tools = %#v", rawTools)
	}
	for _, rawTool := range rawTools {
		tool, _ := rawTool.(map[string]any)
		if tool["type"] == "image_generation" {
			if _, exists := tool["output_format"]; exists {
				t.Fatalf("image tool unexpectedly overrides output_format: %#v", tool)
			}
			if _, exists := tool["partial_images"]; exists {
				t.Fatalf("image tool unexpectedly requests partial_images: %#v", tool)
			}
		}
	}

	list := authenticatedRequest(t, http.MethodGet, server.URL+"/api/v1/conversations/"+conversation.ID+"/messages", cookie, "", "")
	var messagesPayload struct {
		Messages []store.Message `json:"messages"`
	}
	if err := json.NewDecoder(list.Body).Decode(&messagesPayload); err != nil {
		t.Fatal(err)
	}
	list.Body.Close()
	if len(messagesPayload.Messages) != 2 {
		t.Fatalf("messages = %#v", messagesPayload.Messages)
	}
	assistant := messagesPayload.Messages[1]
	if assistant.Status != "completed" || assistant.Model != "gpt-chat" ||
		assistant.ReasoningEffortRequested != "high" || assistant.ReasoningTokens != 3 {
		t.Fatalf("assistant = %#v", assistant)
	}
	responseRecord := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/responses/"+assistant.ID,
		cookie, "", "",
	)
	if responseRecord.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(responseRecord.Body)
		responseRecord.Body.Close()
		t.Fatalf("response record status=%d body=%s", responseRecord.StatusCode, raw)
	}
	var responsePayload struct {
		Message store.Message `json:"message"`
	}
	if err := json.NewDecoder(responseRecord.Body).Decode(&responsePayload); err != nil {
		responseRecord.Body.Close()
		t.Fatal(err)
	}
	responseRecord.Body.Close()
	if responsePayload.Message.ID != assistant.ID ||
		responsePayload.Message.Status != "completed" {
		t.Fatalf("response record = %#v", responsePayload.Message)
	}
	partTypes := make([]string, len(assistant.Parts))
	for index, part := range assistant.Parts {
		partTypes[index] = part.Type
	}
	if got, want := strings.Join(partTypes, ","), "reasoning,reasoning,tool,text,citations,tool,image"; got != want {
		t.Fatalf("assistant part order = %s, want %s", got, want)
	}
	var reasoningPartData struct {
		DurationMS     int64  `json:"durationMs"`
		ProviderItemID string `json:"providerItemId"`
		SummaryIndex   int    `json:"summaryIndex"`
		Completed      bool   `json:"completed"`
	}
	if err := json.Unmarshal(assistant.Parts[0].JSONContent, &reasoningPartData); err != nil {
		t.Fatal(err)
	}
	if reasoningPartData.DurationMS < 1 || reasoningPartData.ProviderItemID != "reasoning_1" ||
		reasoningPartData.SummaryIndex != 0 || !reasoningPartData.Completed {
		t.Fatalf("reasoning metadata = %#v", reasoningPartData)
	}
	var secondReasoningPartData struct {
		ProviderItemID string `json:"providerItemId"`
		SummaryIndex   int    `json:"summaryIndex"`
	}
	if err := json.Unmarshal(assistant.Parts[1].JSONContent, &secondReasoningPartData); err != nil {
		t.Fatal(err)
	}
	if secondReasoningPartData.ProviderItemID != "reasoning_1" ||
		secondReasoningPartData.SummaryIndex != 1 {
		t.Fatalf("second reasoning metadata = %#v", secondReasoningPartData)
	}
	var generatedAttachmentID string
	for _, part := range assistant.Parts {
		if part.Type == "image" {
			generatedAttachmentID = part.AttachmentID
		}
	}
	if generatedAttachmentID == "" {
		t.Fatal("generated image part missing")
	}
	imageResponse := authenticatedRequest(t, http.MethodGet, server.URL+"/api/v1/attachments/"+generatedAttachmentID+"/content", cookie, "", "")
	if imageResponse.StatusCode != http.StatusOK || imageResponse.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("image response status=%d content-type=%q", imageResponse.StatusCode, imageResponse.Header.Get("Content-Type"))
	}
	imageResponse.Body.Close()

	grokConversation := createTestConversation(t, server.URL, cookie, csrf, "grok-4.5", "high")
	grokImageResponse := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/conversations/"+grokConversation.ID+"/responses",
		cookie, csrf,
		`{"text":"Draw a fox","attachmentIds":[],"requestId":"grok-image-1","generateImage":true}`,
	)
	grokStream, _ := io.ReadAll(grokImageResponse.Body)
	grokImageResponse.Body.Close()
	if grokImageResponse.StatusCode != http.StatusOK ||
		!strings.Contains(string(grokStream), "event: response.tool") ||
		!strings.Contains(string(grokStream), "event: response.image") ||
		!strings.Contains(string(grokStream), "event: response.completed") {
		t.Fatalf("Grok image response status=%d body=%s", grokImageResponse.StatusCode, grokStream)
	}
	providerMu.Lock()
	gotDedicatedRequest := dedicatedImageRequest
	providerMu.Unlock()
	if gotDedicatedRequest["model"] != "grok-imagine-image-quality" ||
		gotDedicatedRequest["prompt"] != "Draw a fox" {
		t.Fatalf("dedicated image request = %#v", gotDedicatedRequest)
	}
	for _, forbidden := range []string{"quality", "size", "compression", "partial_images"} {
		if _, exists := gotDedicatedRequest[forbidden]; exists {
			t.Fatalf("dedicated image request unexpectedly set %s: %#v", forbidden, gotDedicatedRequest)
		}
	}

	regeneration := authenticatedRequest(
		t, http.MethodPost, server.URL+"/api/v1/messages/"+assistant.ID+"/regenerate",
		cookie, csrf, `{"requestId":"regenerate-1"}`,
	)
	regenerationBody, _ := io.ReadAll(regeneration.Body)
	regeneration.Body.Close()
	if regeneration.StatusCode != http.StatusOK ||
		!strings.Contains(string(regenerationBody), `"regenerated":true`) ||
		!strings.Contains(string(regenerationBody), "event: response.completed") {
		t.Fatalf("regeneration status=%d body=%s", regeneration.StatusCode, regenerationBody)
	}
	afterRegeneration := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/conversations/"+conversation.ID+"/messages",
		cookie, "", "",
	)
	messagesPayload.Messages = nil
	if err := json.NewDecoder(afterRegeneration.Body).Decode(&messagesPayload); err != nil {
		t.Fatal(err)
	}
	afterRegeneration.Body.Close()
	if len(messagesPayload.Messages) != 3 {
		t.Fatalf("messages after regeneration = %#v", messagesPayload.Messages)
	}
	regenerated := messagesPayload.Messages[2]
	if regenerated.ParentMessageID != messagesPayload.Messages[0].ID || regenerated.Status != "completed" {
		t.Fatalf("regenerated assistant = %#v", regenerated)
	}
	repeatOld := authenticatedRequest(
		t, http.MethodPost, server.URL+"/api/v1/messages/"+assistant.ID+"/regenerate",
		cookie, csrf, `{"requestId":"regenerate-old"}`,
	)
	if repeatOld.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(repeatOld.Body)
		t.Fatalf("old regeneration status=%d body=%s", repeatOld.StatusCode, body)
	}
	repeatOld.Body.Close()

	otherToken, err := createTestSession(dataStore, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	crossUser := authenticatedRequest(t, http.MethodGet, server.URL+"/api/v1/conversations/"+conversation.ID, otherToken, "", "")
	if crossUser.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(crossUser.Body)
		t.Fatalf("cross-user status=%d body=%s", crossUser.StatusCode, body)
	}
	crossUser.Body.Close()
	crossUserImage := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/attachments/"+generatedAttachmentID+"/content",
		otherToken, "", "",
	)
	if crossUserImage.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(crossUserImage.Body)
		t.Fatalf("cross-user image status=%d body=%s", crossUserImage.StatusCode, body)
	}
	crossUserImage.Body.Close()
	regularSetting := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/admin/progressive-summaries",
		otherToken, "", "",
	)
	if regularSetting.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(regularSetting.Body)
		t.Fatalf("regular user setting status=%d body=%s", regularSetting.StatusCode, body)
	}
	regularSetting.Body.Close()

	adminToken, err := createTestSession(dataStore, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminList := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/conversations", adminToken, "", "",
	)
	var adminConversations struct {
		Conversations []store.Conversation `json:"conversations"`
	}
	if err := json.NewDecoder(adminList.Body).Decode(&adminConversations); err != nil {
		t.Fatal(err)
	}
	adminList.Body.Close()
	if adminList.StatusCode != http.StatusOK || len(adminConversations.Conversations) < 2 {
		t.Fatalf("admin conversations status=%d body=%#v", adminList.StatusCode, adminConversations)
	}
	foundOwnedConversation := false
	for _, item := range adminConversations.Conversations {
		if item.ID == conversation.ID && item.UserID == user.ID && item.OwnerUsername == user.Username {
			foundOwnedConversation = true
		}
	}
	if !foundOwnedConversation {
		t.Fatalf("admin list omitted owner metadata: %#v", adminConversations.Conversations)
	}
	adminMessages := authenticatedRequest(
		t, http.MethodGet,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/messages",
		adminToken, "", "",
	)
	if adminMessages.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(adminMessages.Body)
		t.Fatalf("admin message read status=%d body=%s", adminMessages.StatusCode, body)
	}
	adminMessages.Body.Close()
	adminImage := authenticatedRequest(
		t, http.MethodGet,
		server.URL+"/api/v1/attachments/"+generatedAttachmentID+"/content",
		adminToken, "", "",
	)
	if adminImage.StatusCode != http.StatusOK {
		t.Fatalf("admin image read status=%d", adminImage.StatusCode)
	}
	adminImage.Body.Close()
	adminWrite := authenticatedRequest(
		t, http.MethodPatch,
		server.URL+"/api/v1/conversations/"+conversation.ID,
		adminToken, app.csrfToken(adminToken), `{"title":"must not change"}`,
	)
	if adminWrite.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(adminWrite.Body)
		t.Fatalf("admin cross-user write status=%d body=%s", adminWrite.StatusCode, body)
	}
	adminWrite.Body.Close()

	adminSetting := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/admin/progressive-summaries",
		adminToken, "", "",
	)
	var settingPayload struct {
		ProgressiveSummary struct {
			Mode           string `json:"mode"`
			EffectiveState string `json:"effectiveState"`
		} `json:"progressiveSummary"`
	}
	if err := json.NewDecoder(adminSetting.Body).Decode(&settingPayload); err != nil {
		t.Fatal(err)
	}
	adminSetting.Body.Close()
	if adminSetting.StatusCode != http.StatusOK ||
		settingPayload.ProgressiveSummary.Mode != "auto" ||
		settingPayload.ProgressiveSummary.EffectiveState != "active" {
		t.Fatalf("admin setting = status %d payload %#v", adminSetting.StatusCode, settingPayload)
	}

	adminSettingUpdate := authenticatedRequest(
		t, http.MethodPut, server.URL+"/api/v1/admin/progressive-summaries",
		adminToken, app.csrfToken(adminToken), `{"mode":"off"}`,
	)
	if err := json.NewDecoder(adminSettingUpdate.Body).Decode(&settingPayload); err != nil {
		t.Fatal(err)
	}
	adminSettingUpdate.Body.Close()
	if adminSettingUpdate.StatusCode != http.StatusOK ||
		settingPayload.ProgressiveSummary.Mode != "off" ||
		settingPayload.ProgressiveSummary.EffectiveState != "disabled" {
		t.Fatalf("updated admin setting = status %d payload %#v", adminSettingUpdate.StatusCode, settingPayload)
	}

	adminRecheck := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/admin/progressive-summaries/recheck",
		adminToken, app.csrfToken(adminToken), "",
	)
	if adminRecheck.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(adminRecheck.Body)
		t.Fatalf("admin recheck status=%d body=%s", adminRecheck.StatusCode, body)
	}
	adminRecheck.Body.Close()
	audit, err := dataStore.ListServiceSettingAudit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) < 2 || audit[0].Action != "recheck" || audit[1].Action != "update" ||
		audit[0].ActorUserID != admin.ID || audit[1].ActorUserID != admin.ID {
		t.Fatalf("service setting audit = %#v", audit)
	}

	generatedRoot := filepath.Join(dataDir, "generated")
	if _, err := os.Stat(generatedRoot); err != nil {
		t.Fatalf("generated image was not written: %v", err)
	}
	deleted := authenticatedRequest(
		t, http.MethodDelete, server.URL+"/api/v1/conversations/"+conversation.ID,
		cookie, csrf, "",
	)
	if deleted.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(deleted.Body)
		t.Fatalf("delete conversation status=%d body=%s", deleted.StatusCode, body)
	}
	deleted.Body.Close()
	deletedImage := authenticatedRequest(
		t, http.MethodGet, server.URL+"/api/v1/attachments/"+generatedAttachmentID+"/content",
		cookie, "", "",
	)
	if deletedImage.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted conversation image status=%d", deletedImage.StatusCode)
	}
	deletedImage.Body.Close()
	deletedGrok := authenticatedRequest(
		t, http.MethodDelete, server.URL+"/api/v1/conversations/"+grokConversation.ID,
		cookie, csrf, "",
	)
	if deletedGrok.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(deletedGrok.Body)
		t.Fatalf("delete Grok conversation status=%d body=%s", deletedGrok.StatusCode, body)
	}
	deletedGrok.Body.Close()
	var remainingGenerated int
	_ = filepath.WalkDir(generatedRoot, func(_ string, entry os.DirEntry, _ error) error {
		if entry != nil && !entry.IsDir() {
			remainingGenerated++
		}
		return nil
	})
	if remainingGenerated != 0 {
		t.Fatalf("generated files after conversation delete = %d", remainingGenerated)
	}
}

func TestProgressiveSummaryFallbackRetriesOnlyTheRejectedAttempt(t *testing.T) {
	var providerMu sync.Mutex
	requests := make([]map[string]any, 0, 3)
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeReasoningTestModel(w)
		case "/v1/responses":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			providerMu.Lock()
			requests = append(requests, request)
			attempt := len(requests)
			providerMu.Unlock()
			if attempt == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{
					"type":"invalid_parameter",
					"message":"Unknown parameter: stream_options.reasoning_summary_delivery"
				}}`)
				return
			}
			writeCompletedTextStream(w, "resp_fallback", "Fallback succeeded.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockProvider.Close()

	app, server, _, cookie, csrf := startReasoningIntegrationApp(t, mockProvider.URL)
	conversation := createTestConversation(
		t, server.URL, cookie, csrf, "gpt-reasoning", "high",
	)
	first := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie, csrf, `{"text":"first","attachmentIds":[],"requestId":"fallback-1"}`,
	)
	firstBody, _ := io.ReadAll(first.Body)
	first.Body.Close()
	if first.StatusCode != http.StatusOK ||
		!strings.Contains(string(firstBody), "event: response.completed") {
		t.Fatalf("first response status=%d body=%s", first.StatusCode, firstBody)
	}

	providerMu.Lock()
	if len(requests) != 2 {
		providerMu.Unlock()
		t.Fatalf("provider attempts = %d, want 2", len(requests))
	}
	firstOptions, _ := requests[0]["stream_options"].(map[string]any)
	_, secondHasOptions := requests[1]["stream_options"]
	providerMu.Unlock()
	if firstOptions["reasoning_summary_delivery"] != "sequential_cutoff" ||
		secondHasOptions {
		t.Fatalf("fallback request options first=%#v secondHasOptions=%v", firstOptions, secondHasOptions)
	}
	statuses := app.summaries.Snapshot(app.cfg.Provider.BaseURL.String())
	if len(statuses) != 1 || statuses[0].State != progressivesummary.StateCooldown {
		t.Fatalf("compatibility status = %#v", statuses)
	}

	second := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie, csrf, `{"text":"second","attachmentIds":[],"requestId":"fallback-2"}`,
	)
	secondBody, _ := io.ReadAll(second.Body)
	second.Body.Close()
	if second.StatusCode != http.StatusOK ||
		!strings.Contains(string(secondBody), "event: response.completed") {
		t.Fatalf("second response status=%d body=%s", second.StatusCode, secondBody)
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("provider attempts after cooldown request = %d, want 3", len(requests))
	}
	if _, exists := requests[2]["stream_options"]; exists {
		t.Fatalf("cooldown request unexpectedly contained stream_options: %#v", requests[2])
	}
}

func TestAcceptedProviderStreamIsNeverAutomaticallyRetried(t *testing.T) {
	var providerMu sync.Mutex
	attempts := 0
	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeReasoningTestModel(w)
		case "/v1/responses":
			providerMu.Lock()
			attempts++
			providerMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.created","response":{"id":"resp_incomplete"}}`+
				"\n\n")
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.reasoning_summary_text.delta","item_id":"reasoning_1","summary_index":0,"delta":"Partial summary."}`+
				"\n\n")
			// Deliberately close without response.completed or [DONE].
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockProvider.Close()

	_, server, _, cookie, csrf := startReasoningIntegrationApp(t, mockProvider.URL)
	conversation := createTestConversation(
		t, server.URL, cookie, csrf, "gpt-reasoning", "high",
	)
	response := authenticatedRequest(
		t, http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		cookie, csrf, `{"text":"do not retry","attachmentIds":[],"requestId":"accepted-1"}`,
	)
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), `"code":"provider_stream_incomplete"`) {
		t.Fatalf("response status=%d body=%s", response.StatusCode, body)
	}
	providerMu.Lock()
	defer providerMu.Unlock()
	if attempts != 1 {
		t.Fatalf("accepted provider stream attempts = %d, want exactly 1", attempts)
	}
}

func TestResponseContinuesAndPersistsAfterViewerDisconnects(t *testing.T) {
	var providerMu sync.Mutex
	providerAttempts := 0
	providerStarted := make(chan struct{})
	var providerStartedOnce sync.Once
	releaseProvider := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseProvider) }) }
	t.Cleanup(release)

	mockProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			writeReasoningTestModel(w)
		case "/v1/responses":
			providerMu.Lock()
			providerAttempts++
			providerMu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.created","response":{"id":"resp_background"}}`+
				"\n\n")
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.output_text.delta","delta":"Saved partial. "}`+
				"\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			providerStartedOnce.Do(func() { close(providerStarted) })
			<-releaseProvider
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.output_text.delta","delta":"Finished later."}`+
				"\n\n")
			_, _ = io.WriteString(w, "data: "+
				`{"type":"response.completed","response":{"id":"resp_background","status":"completed"}}`+
				"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockProvider.Close()

	app, server, dataStore, cookie, csrf := startReasoningIntegrationApp(
		t, mockProvider.URL,
	)
	conversation := createTestConversation(
		t, server.URL, cookie, csrf, "gpt-reasoning", "high",
	)
	request, err := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/v1/conversations/"+conversation.ID+"/responses",
		strings.NewReader(
			`{"text":"continue in background","attachmentIds":[],"requestId":"background-1"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Origin", "http://chat.test")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(cookie)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("response status=%d body=%s", response.StatusCode, raw)
	}
	// Closing the viewer is deliberately not an explicit cancellation.
	response.Body.Close()

	select {
	case <-providerStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start after the viewer disconnected")
	}
	user, err := dataStore.UserByUsername(context.Background(), "reasoning-user")
	if err != nil {
		t.Fatal(err)
	}
	waitForAssistant := func(
		condition func(store.Message) bool,
	) store.Message {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			current, listErr := dataStore.ListMessages(
				context.Background(), user.ID, conversation.ID,
			)
			if listErr == nil {
				for _, message := range current {
					if message.Role == "assistant" && condition(message) {
						return message
					}
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("assistant state did not become observable before the deadline")
		return store.Message{}
	}
	partial := waitForAssistant(func(message store.Message) bool {
		return message.Status == "streaming" &&
			len(message.Parts) == 1 &&
			message.Parts[0].TextContent == "Saved partial. "
	})
	if partial.ErrorCode != "" {
		t.Fatalf("partial response error = %q", partial.ErrorCode)
	}

	release()
	completed := waitForAssistant(func(message store.Message) bool {
		return message.Status == "completed"
	})
	if len(completed.Parts) != 1 ||
		completed.Parts[0].TextContent != "Saved partial. Finished later." {
		t.Fatalf("completed response = %#v", completed)
	}
	providerMu.Lock()
	attempts := providerAttempts
	providerMu.Unlock()
	if attempts != 1 {
		t.Fatalf("provider attempts = %d, want exactly 1", attempts)
	}
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(), time.Second,
	)
	defer cancelShutdown()
	if err := app.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}

func startReasoningIntegrationApp(
	t *testing.T,
	providerBaseURL string,
) (*Server, *httptest.Server, *store.Store, *http.Cookie, string) {
	t.Helper()
	dataDir := t.TempDir()
	dataStore, err := store.Open(context.Background(), filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	passwordHash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateUser(
		context.Background(), "reasoning-user", "Reasoning User", passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse(providerBaseURL + "/v1")
	cfg := config.Config{
		Environment: "test", HTTPAddr: ":0", BaseURL: baseURL, DataDir: dataDir,
		DatabasePath: filepath.Join(dataDir, "app.db"),
		AppSecret:    []byte("01234567890123456789012345678901"),
		SessionTTL:   time.Hour, SessionCookieName: "owui_session",
		Provider: config.Provider{
			Kind: "cpa", BaseURL: providerURL, APIKey: "provider-test-key",
			DefaultModel: "gpt-reasoning", ModelsTimeout: time.Second,
			DefaultReasoningEffort: "high", UnknownModelContextTokens: 128000,
			RequestBodyMaxBytes: 50 << 20,
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal: 4, MaxConcurrentPerUser: 2,
			MaxQueuedPerUser: 2, QueueTimeout: time.Second,
		},
		Tools: config.Tools{WebSearchEnabled: true, ImageGenerationEnabled: true},
	}
	client := provider.NewClient(cfg.Provider, "test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	app := New(cfg, dataStore, client, logger)
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	cookie, csrf := loginTestUser(t, server.URL, "reasoning-user", "test-password")
	return app, server, dataStore, cookie, csrf
}

func writeReasoningTestModel(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"models":[{
		"slug":"gpt-reasoning","display_name":"GPT Reasoning",
		"context_window":128000,"input_modalities":["text"],
		"supported_reasoning_levels":[{"effort":"high"}],
		"default_reasoning_level":"high","priority":1
	}]}`)
}

func writeCompletedTextStream(w http.ResponseWriter, responseID, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	encodedText, _ := json.Marshal(text)
	_, _ = io.WriteString(w, "data: "+
		`{"type":"response.output_text.delta","delta":`+string(encodedText)+`}`+
		"\n\n")
	_, _ = io.WriteString(w, "data: "+
		`{"type":"response.completed","response":{"id":"`+responseID+`","status":"completed"}}`+
		"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func loginTestUser(t *testing.T, baseURL, username, password string) (*http.Cookie, string) {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/auth/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://chat.test")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("login status=%d body=%s", response.StatusCode, raw)
	}
	var payload struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(response.Cookies()) != 1 {
		t.Fatalf("login cookies = %#v", response.Cookies())
	}
	return response.Cookies()[0], payload.CSRFToken
}

func createTestConversation(t *testing.T, baseURL string, cookie *http.Cookie, csrf, model, effort string) store.Conversation {
	t.Helper()
	body := `{"title":"Test","model":"` + model + `","reasoningEffort":"` + effort + `"}`
	response := authenticatedRequest(t, http.MethodPost, baseURL+"/api/v1/conversations", cookie, csrf, body)
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("create conversation status=%d body=%s", response.StatusCode, raw)
	}
	var payload struct {
		Conversation store.Conversation `json:"conversation"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Conversation
}

func authenticatedRequest(t *testing.T, method, endpoint string, cookie any, csrf, body string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	switch value := cookie.(type) {
	case *http.Cookie:
		request.AddCookie(value)
	case string:
		request.AddCookie(&http.Cookie{Name: "owui_session", Value: value})
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
		request.Header.Set("Origin", "http://chat.test")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func createTestSession(dataStore *store.Store, userID string) (string, error) {
	token := "test-session-" + userID
	_, err := dataStore.CreateSession(context.Background(), userID, token, "test", time.Hour)
	return token, err
}
