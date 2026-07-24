package activecontext

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestEstimateTextIsConservativeForChineseAndASCII(t *testing.T) {
	if got := EstimateText(strings.Repeat("a", 300)); got < 100 {
		t.Fatalf("ASCII estimate = %d", got)
	}
	if got := EstimateText(strings.Repeat("你", 100)); got < 100 {
		t.Fatalf("Chinese estimate = %d", got)
	}
	for name, sample := range map[string]string{
		"url":  "https://example.com/search?q=hello%20world&lang=zh-CN",
		"code": "func main() { fmt.Println(`hello`) }\n// braces: {}[]",
		"json": `{"tool":"web_search","query":"上海天气","results":[1,2,3]}`,
	} {
		want := (len(sample) + 1) / 2
		if got := EstimateText(sample); got != want {
			t.Errorf("%s estimate = %d, want %d", name, got, want)
		}
	}
}

func TestChooseRecentStartKeepsUserBoundary(t *testing.T) {
	messages := []store.Message{
		{Role: "user", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("a", 100)}}},
		{Role: "assistant", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("b", 100)}}},
		{Role: "user", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("c", 100)}}},
		{Role: "assistant", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("d", 100)}}},
		{Role: "user", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("e", 100)}}},
		{Role: "assistant", Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("f", 100)}}},
	}
	start, err := (&Manager{}).chooseRecentStart(context.Background(), "user", messages, 300)
	if err != nil {
		t.Fatal(err)
	}
	if start <= 0 || messages[start].Role != "user" {
		t.Fatalf("start = %d, role = %q", start, messages[start].Role)
	}
}

func TestPrepareCreatesDurableCheckpointAndKeepsOriginalMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{
			"id":"checkpoint-response","status":"completed",
			"output":[{"type":"message","content":[{"type":"output_text","text":"## 长期用户偏好\n无\n## 事实与实体\n已确认事实\n## 已作决定与约束\n无\n## 当前话题状态\n继续测试\n## 未决问题与下一步\n无\n## 重要附件、工具与引用\n无"}]}],
			"usage":{"input_tokens":100,"output_tokens":20}
		}`)
	}))
	defer upstream.Close()

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "app.db")
	dataStore, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	user, err := dataStore.CreateUser(ctx, "checkpoint-user", "Checkpoint", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Long chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	for index := range 4 {
		_, assistant, err := dataStore.BeginResponse(
			ctx, user.ID, conversation.ID, "request-"+string(rune('a'+index)),
			strings.Repeat("user context ", 45), conversation.Model, "auto", "", nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dataStore.CompleteAssistant(ctx, user.ID, assistant.ID, store.AssistantResult{
			Status: "completed",
			Parts: []store.NewMessagePart{{
				Type: "text", TextContent: strings.Repeat("assistant context ", 35),
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	allMessages, err := dataStore.ListMessages(ctx, user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	providerURL, _ := url.Parse(upstream.URL + "/v1")
	client := provider.NewClient(config.Provider{
		BaseURL: providerURL, APIKey: "test", RequestBodyMaxBytes: 1 << 20,
		RequestTempDir: filepath.Join(t.TempDir(), "provider"),
	}, "test")
	manager := New(dataStore, client)
	var statuses []string
	result, err := manager.Prepare(
		ctx, user.ID, conversation,
		provider.Model{ID: "gpt-test", ContextWindow: 3000},
		"", allMessages, allMessages[len(allMessages)-1].ID,
		func(status string, _ map[string]any) error {
			statuses = append(statuses, status)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Checkpoint == nil || result.Checkpoint.SummaryText == "" ||
		!result.CompactionAttempted || len(result.Messages) >= len(allMessages) {
		t.Fatalf("Prepare() result = %#v", result)
	}
	if strings.Join(statuses, ",") != "started,completed" {
		t.Fatalf("statuses = %#v", statuses)
	}
	completeHistory, err := dataStore.ListMessages(ctx, user.ID, conversation.ID)
	if err != nil || len(completeHistory) != len(allMessages) {
		t.Fatalf("original messages changed: len=%d err=%v", len(completeHistory), err)
	}
	saved, err := dataStore.LatestCheckpoint(ctx, user.ID, conversation.ID)
	if err != nil || saved.ID != result.Checkpoint.ID {
		t.Fatalf("saved checkpoint = %#v, %v", saved, err)
	}
}

func TestPrepareCompactsAtSerializedByteSoftLine(t *testing.T) {
	var compactionCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compactionCalls++
		_, _ = io.WriteString(w, `{
			"id":"checkpoint-response","status":"completed",
			"output":[{"type":"message","content":[{"type":"output_text","text":"## 长期用户偏好\n无\n## 事实与实体\n三张历史图片\n## 已作决定与约束\n无\n## 当前话题状态\n继续图像分析\n## 未决问题与下一步\n无\n## 重要附件、工具与引用\n保留附件 ID"}]}],
			"usage":{"input_tokens":50,"output_tokens":20}
		}`)
	}))
	defer upstream.Close()

	ctx := context.Background()
	dataStore := openContextTestStore(t)
	user, err := dataStore.CreateUser(ctx, "byte-user", "Byte User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Images", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		attachmentID := "attachment-" + string(rune('a'+index))
		if _, err := dataStore.CreateAttachment(ctx, store.Attachment{
			ID: attachmentID, UserID: user.ID, Kind: "upload", MediaType: "image/png",
			ByteSize: 12 * 1024 * 1024, SHA256: "test", StoragePath: "unused/" + attachmentID,
		}); err != nil {
			t.Fatal(err)
		}
		_, assistant, err := dataStore.BeginResponse(
			ctx, user.ID, conversation.ID, "byte-request-"+string(rune('a'+index)),
			"image", conversation.Model, "auto", "", []string{attachmentID},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dataStore.CompleteAssistant(ctx, user.ID, assistant.ID, store.AssistantResult{
			Status: "completed", Parts: []store.NewMessagePart{{Type: "text", TextContent: "seen"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := dataStore.ListMessages(ctx, user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	providerURL, _ := url.Parse(upstream.URL + "/v1")
	client := provider.NewClient(config.Provider{
		BaseURL: providerURL, APIKey: "test", RequestBodyMaxBytes: 50 << 20,
		RequestTempDir: filepath.Join(t.TempDir(), "provider"),
	}, "test")
	result, err := New(dataStore, client).Prepare(
		ctx, user.ID, conversation,
		provider.Model{ID: "gpt-test", ContextWindow: 4_000_000},
		"", messages, messages[len(messages)-1].ID, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.CompactionAttempted || result.Checkpoint == nil || compactionCalls != 1 {
		t.Fatalf("byte compaction result = %#v, calls=%d", result, compactionCalls)
	}
	if result.EstimatedBytes >= hardRequestBytes {
		t.Fatalf("estimated bytes after compaction = %d", result.EstimatedBytes)
	}
}

func TestReplayableMessagesExcludeInFlightAssistant(t *testing.T) {
	messages := []store.Message{
		{ID: "user", Role: "user", Status: "completed"},
		{ID: "assistant", Role: "assistant", Status: "streaming"},
	}
	got := replayableMessages(messages)
	if len(got) != 1 || got[0].ID != "user" {
		t.Fatalf("replayable messages = %#v", got)
	}
}

func TestCompactionFailureContinuesOnlyBelowHardTokenLine(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	ctx := context.Background()
	dataStore := openContextTestStore(t)
	user, err := dataStore.CreateUser(ctx, "threshold-user", "Threshold", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Threshold", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]store.Message, 0, 6)
	for index := range 3 {
		messages = append(messages,
			store.Message{
				ID: "user-" + string(rune('a'+index)), Role: "user", Status: "completed",
				Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("u", 160)}},
			},
			store.Message{
				ID: "assistant-" + string(rune('a'+index)), Role: "assistant", Status: "completed",
				Parts: []store.MessagePart{{Type: "text", TextContent: strings.Repeat("a", 160)}},
			},
		)
	}
	providerURL, _ := url.Parse(upstream.URL + "/v1")
	client := provider.NewClient(config.Provider{
		BaseURL: providerURL, APIKey: "test", RequestBodyMaxBytes: 1 << 20,
		RequestTempDir: filepath.Join(t.TempDir(), "provider"),
	}, "test")
	manager := New(dataStore, client)

	var softFailedContinuing bool
	soft, err := manager.Prepare(
		ctx, user.ID, conversation, provider.Model{ID: "gpt-test", ContextWindow: 600},
		"", messages, messages[len(messages)-1].ID,
		func(status string, data map[string]any) error {
			if status == "failed" {
				softFailedContinuing, _ = data["continuing"].(bool)
			}
			return nil
		},
	)
	if err != nil || soft.CompactionWarning == nil || !softFailedContinuing {
		t.Fatalf("soft-line result=%#v err=%v continuing=%v", soft, err, softFailedContinuing)
	}

	var hardFailedContinuing = true
	_, err = manager.Prepare(
		ctx, user.ID, conversation, provider.Model{ID: "gpt-test", ContextWindow: 580},
		"", messages, messages[len(messages)-1].ID,
		func(status string, data map[string]any) error {
			if status == "failed" {
				hardFailedContinuing, _ = data["continuing"].(bool)
			}
			return nil
		},
	)
	if !errors.Is(err, ErrContextTooLarge) || hardFailedContinuing {
		t.Fatalf("hard-line error=%v continuing=%v", err, hardFailedContinuing)
	}
}

func openContextTestStore(t *testing.T) *store.Store {
	t.Helper()
	dataStore, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	return dataStore
}
