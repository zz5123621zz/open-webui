package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestConcurrentResponseStartsSerializeWithoutBusyErrors(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	userA, err := dataStore.CreateUser(ctx, "concurrent-a", "Concurrent A", "hash")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := dataStore.CreateUser(ctx, "concurrent-b", "Concurrent B", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversations := make([]Conversation, 0, 4)
	for _, userID := range []string{userA.ID, userA.ID, userB.ID, userB.ID} {
		conversation, err := dataStore.CreateConversation(
			ctx, userID, "Concurrent", "gpt-test", "auto",
		)
		if err != nil {
			t.Fatal(err)
		}
		conversations = append(conversations, conversation)
	}
	start := make(chan struct{})
	errorsByJob := make(chan error, len(conversations))
	var workers sync.WaitGroup
	for index, conversation := range conversations {
		workers.Add(1)
		go func(index int, conversation Conversation) {
			defer workers.Done()
			<-start
			_, _, err := dataStore.BeginResponse(
				ctx, conversation.UserID, conversation.ID,
				"concurrent-request-"+string(rune('a'+index)),
				"hello", "gpt-test", "auto", "", nil,
			)
			errorsByJob <- err
		}(index, conversation)
	}
	close(start)
	workers.Wait()
	close(errorsByJob)
	for err := range errorsByJob {
		if err != nil {
			t.Fatalf("concurrent BeginResponse() error = %v", err)
		}
	}
}

func TestReadyAndBackup(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	if _, err := dataStore.CreateUser(ctx, "backup-user", "Backup User", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Ready(ctx); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	destination := filepath.Join(t.TempDir(), "daily", "snapshot.db")
	if err := dataStore.Backup(ctx, destination); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 600", info.Mode().Perm())
	}
	if err := dataStore.Backup(ctx, destination); err == nil {
		t.Fatal("Backup() overwrote an existing snapshot")
	}
	backup, err := Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if _, err := backup.UserByUsername(ctx, "backup-user"); err != nil {
		t.Fatalf("backup user lookup: %v", err)
	}
}

func TestUserAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)

	user, err := store.CreateUser(ctx, "Alice", "Alice", "hash")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if _, err := store.CreateUser(ctx, "alice", "Other", "hash"); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("duplicate CreateUser() error = %v", err)
	}

	session, err := store.CreateSession(ctx, user.ID, "secret-token", "test-agent", time.Hour)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	got, err := store.SessionByToken(ctx, "secret-token")
	if err != nil {
		t.Fatalf("SessionByToken() error = %v", err)
	}
	if got.User.ID != user.ID || got.ID != session.ID {
		t.Fatalf("SessionByToken() = %#v", got)
	}
	if err := store.DeleteSession(ctx, session.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SessionByToken(ctx, "secret-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted SessionByToken() error = %v", err)
	}
}

func TestConversationQueriesAreUserScoped(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	userA, err := dataStore.CreateUser(ctx, "alice", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := dataStore.CreateUser(ctx, "bob", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, userA.ID, "", "gpt-chat", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ConversationByID(ctx, userB.ID, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user ConversationByID() error = %v", err)
	}
	if _, err := dataStore.DeleteConversation(ctx, userB.ID, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user DeleteConversation() error = %v", err)
	}
	if _, err := dataStore.ConversationByID(ctx, userA.ID, conversation.ID); err != nil {
		t.Fatalf("owner ConversationByID() error = %v", err)
	}
	archived, err := dataStore.SetConversationArchived(ctx, userA.ID, conversation.ID, true)
	if err != nil || archived.ArchivedAt == 0 {
		t.Fatalf("SetConversationArchived() = %#v, %v", archived, err)
	}
	if _, err := dataStore.ConversationByID(ctx, userA.ID, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active lookup of archived conversation error = %v", err)
	}
	archivedList, err := dataStore.ListConversationsByArchive(ctx, userA.ID, 100, true)
	if err != nil || len(archivedList) != 1 || archivedList[0].ID != conversation.ID {
		t.Fatalf("archived conversations = %#v, %v", archivedList, err)
	}
	if _, err := dataStore.SetConversationArchived(ctx, userA.ID, conversation.ID, false); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantProviderItemsAndToolEventsPersistTransactionally(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "replay-user", "Replay", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Replay", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	_, assistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "replay-request", "hello",
		"gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	toolJSON := json.RawMessage(`{"callId":"search-1","type":"web_search","status":"completed","data":{"query":"safe"},"durationMs":25}`)
	reasoningJSON := json.RawMessage(`{"type":"reasoning","id":"reasoning-1","encrypted_content":"opaque"}`)
	completed, err := dataStore.CompleteAssistant(ctx, user.ID, assistant.ID, AssistantResult{
		Status: "completed",
		Parts:  []NewMessagePart{{Type: "tool", JSONContent: toolJSON}},
		ProviderItems: []NewProviderItem{{
			ItemType: "reasoning", ReplayJSON: reasoningJSON,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.ProviderItems) != 1 ||
		string(completed.ProviderItems[0].ReplayJSON) != string(reasoningJSON) {
		t.Fatalf("provider items = %#v", completed.ProviderItems)
	}
	var toolCount int
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tool_events WHERE message_id = ?
	`, assistant.ID).Scan(&toolCount); err != nil {
		t.Fatal(err)
	}
	if toolCount != 1 {
		t.Fatalf("tool event count = %d", toolCount)
	}
}

func TestCheckpointIsIdempotentAndRejectsStaleConversationHead(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "checkpoint-owner", "Checkpoint", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Checkpoint", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	firstUser, firstAssistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "checkpoint-a", "first",
		"gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CompleteAssistant(ctx, user.ID, firstAssistant.ID, AssistantResult{
		Status: "completed", Parts: []NewMessagePart{{Type: "text", TextContent: "answer"}},
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint := ContextCheckpoint{
		ConversationID: conversation.ID, BoundaryMessageID: firstUser.ID,
		SourceFirstMessageID: firstUser.ID, SourceLastMessageID: firstUser.ID,
		Model: "gpt-test", SummaryText: "summary", Status: "completed",
		ExpectedHeadMessageID: firstAssistant.ID,
	}
	first, err := dataStore.CreateCheckpoint(ctx, user.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dataStore.CreateCheckpoint(ctx, user.ID, checkpoint)
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent checkpoint = %#v, %v", second, err)
	}
	secondUser, _, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "checkpoint-b", "second",
		"gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stale := checkpoint
	stale.BoundaryMessageID = secondUser.ID
	stale.SourceFirstMessageID = secondUser.ID
	stale.SourceLastMessageID = secondUser.ID
	if _, err := dataStore.CreateCheckpoint(ctx, user.ID, stale); !errors.Is(err, ErrConversationChanged) {
		t.Fatalf("stale CreateCheckpoint() error = %v", err)
	}
}
