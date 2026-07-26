package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestAdministratorRolePersistsAcrossSessionsAndLists(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	admin, err := dataStore.CreateUserWithRole(ctx, "admin", "Administrator", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateUser(ctx, "member", "Member", "hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.CreateSession(ctx, admin.ID, "admin-token", "test-agent", time.Hour); err != nil {
		t.Fatal(err)
	}
	session, err := dataStore.SessionByToken(ctx, "admin-token")
	if err != nil {
		t.Fatal(err)
	}
	if session.User.Role != "admin" {
		t.Fatalf("session role = %q, want admin", session.User.Role)
	}
	users, err := dataStore.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Username != "admin" || users[0].Role != "admin" {
		t.Fatalf("ListUsers() = %#v", users)
	}
}

func TestEmptyConversationReuseLimitAndPinProtection(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "lifecycle", "Lifecycle", "hash")
	if err != nil {
		t.Fatal(err)
	}
	firstBlank, err := dataStore.CreateConversationWithLimit(
		ctx, user.ID, "New chat", "gpt-one", "high", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondBlank, err := dataStore.CreateConversationWithLimit(
		ctx, user.ID, "New chat", "gpt-two", "medium", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondBlank.ID != firstBlank.ID || secondBlank.Model != "gpt-two" {
		t.Fatalf("empty conversation was not reused: first=%#v second=%#v", firstBlank, secondBlank)
	}

	first, err := dataStore.UpdateConversation(
		ctx, user.ID, firstBlank.ID, "First", "gpt-two", "medium",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetConversationPinned(ctx, user.ID, first.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	second, err := dataStore.CreateConversationWithLimit(
		ctx, user.ID, "Second", "gpt-two", "medium", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetConversationPinned(ctx, user.ID, second.ID, true, 1); !errors.Is(err, ErrPinLimit) {
		t.Fatalf("second pin error = %v, want ErrPinLimit", err)
	}
	if _, err := dataStore.db.ExecContext(
		ctx, `UPDATE conversations SET updated_at = 1 WHERE id = ?`, second.ID,
	); err != nil {
		t.Fatal(err)
	}
	third, err := dataStore.CreateConversationWithLimit(
		ctx, user.ID, "Third", "gpt-two", "medium", 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == "" {
		t.Fatal("third conversation was not created")
	}
	if _, err := dataStore.ConversationByID(ctx, user.ID, first.ID); err != nil {
		t.Fatalf("pinned conversation was retained unexpectedly: %v", err)
	}
	retained, err := dataStore.OwnedConversationByID(ctx, user.ID, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retained.ArchivedAt == 0 || retained.RetentionReason != "conversation_limit" {
		t.Fatalf("automatically retained conversation = %#v", retained)
	}
	active, err := dataStore.ListConversations(ctx, user.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 || active[0].ID != first.ID {
		t.Fatalf("active conversations = %#v", active)
	}
}

func TestStorageQuotaExcludesRetainedAndPurgeRemovesIt(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "quota", "Quota", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Images", "gpt", "high")
	if err != nil {
		t.Fatal(err)
	}
	attachment := Attachment{
		ID: "attachment-one", UserID: user.ID, ConversationID: conversation.ID,
		Kind: "upload", MediaType: "image/png", ByteSize: 60,
		SHA256: "hash-one", StoragePath: "uploads/quota/one.png",
	}
	if _, err := dataStore.CreateAttachmentWithinQuota(ctx, attachment, 100); err != nil {
		t.Fatal(err)
	}
	second := attachment
	second.ID = "attachment-two"
	second.ByteSize = 50
	second.SHA256 = "hash-two"
	second.StoragePath = "uploads/quota/two.png"
	if _, err := dataStore.CreateAttachmentWithinQuota(ctx, second, 100); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("quota error = %v, want ErrStorageQuota", err)
	}
	if _, err := dataStore.SetConversationArchivedWithPolicy(
		ctx, user.ID, conversation.ID, true, 30, 100,
	); err != nil {
		t.Fatal(err)
	}
	status, err := dataStore.StorageStatus(ctx, user.ID, 100, 30, 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	if status.UsedBytes != 0 || status.RetainedBytes != 60 {
		t.Fatalf("retained storage status = %#v", status)
	}
	if _, err := dataStore.SetConversationArchivedWithPolicy(
		ctx, user.ID, conversation.ID, false, 30, 50,
	); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("restore quota error = %v, want ErrStorageQuota", err)
	}
	if _, err := dataStore.db.ExecContext(
		ctx, `UPDATE conversations SET archived_at = 1 WHERE id = ?`, conversation.ID,
	); err != nil {
		t.Fatal(err)
	}
	paths, deleted, err := dataStore.PurgeExpiredRetained(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || len(paths) != 1 || paths[0] != attachment.StoragePath {
		t.Fatalf("purge result paths=%#v deleted=%d", paths, deleted)
	}
	if _, err := dataStore.OwnedConversationByID(ctx, user.ID, conversation.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged conversation lookup error = %v", err)
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
	messageJSON := json.RawMessage(`{"type":"message","id":"message-1","content":[]}`)
	completed, err := dataStore.CompleteAssistant(ctx, user.ID, assistant.ID, AssistantResult{
		Status: "completed",
		Parts:  []NewMessagePart{{Type: "tool", JSONContent: toolJSON}},
		ProviderItems: []NewProviderItem{
			{ItemType: "reasoning", ReplayJSON: reasoningJSON},
			{ItemType: "message", ReplayJSON: messageJSON},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.ProviderItems) != 1 ||
		string(completed.ProviderItems[0].ReplayJSON) != string(messageJSON) {
		t.Fatalf("provider items = %#v", completed.ProviderItems)
	}
	if strings.Contains(string(completed.ProviderItems[0].ReplayJSON), "encrypted_content") {
		t.Fatalf("encrypted reasoning was persisted: %#v", completed.ProviderItems)
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

func TestAssistantProgressSnapshotsReplaceEarlierEvidence(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "progress-user", "Progress", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(
		ctx, user.ID, "Progress", "gpt-test", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, assistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "progress-request", "hello",
		"gpt-test", "high", "high", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveAssistantProgress(
		ctx, user.ID, assistant.ID, AssistantResult{
			ProviderResponseID: "response-1",
			Parts: []NewMessagePart{
				{Type: "text", TextContent: "partial"},
				{
					Type: "tool",
					JSONContent: json.RawMessage(
						`{"callId":"search-1","type":"web_search","status":"in_progress","data":{"query":"safe"}}`,
					),
				},
			},
			ProviderItems: []NewProviderItem{{
				ItemType: "message",
				ReplayJSON: json.RawMessage(
					`{"type":"message","id":"provider-message-1","content":[]}`,
				),
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	progress, err := dataStore.MessageByID(ctx, user.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Status != "streaming" || len(progress.Parts) != 2 ||
		progress.Parts[0].TextContent != "partial" ||
		len(progress.ProviderItems) != 1 {
		t.Fatalf("first progress snapshot = %#v", progress)
	}

	if _, err := dataStore.SaveAssistantProgress(
		ctx, user.ID, assistant.ID, AssistantResult{
			ProviderResponseID: "response-1",
			Parts: []NewMessagePart{{
				Type: "text", TextContent: "partial replacement",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	progress, err = dataStore.MessageByID(ctx, user.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Parts) != 1 ||
		progress.Parts[0].TextContent != "partial replacement" ||
		len(progress.ProviderItems) != 0 {
		t.Fatalf("replacement progress snapshot = %#v", progress)
	}
	var toolCount int
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tool_events WHERE message_id = ?
	`, assistant.ID).Scan(&toolCount); err != nil {
		t.Fatal(err)
	}
	if toolCount != 0 {
		t.Fatalf("stale tool event count = %d", toolCount)
	}

	completed, err := dataStore.CompleteAssistant(
		ctx, user.ID, assistant.ID, AssistantResult{
			Status: "completed",
			Parts: []NewMessagePart{{
				Type: "text", TextContent: "final",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || len(completed.Parts) != 1 ||
		completed.Parts[0].TextContent != "final" {
		t.Fatalf("completed snapshot = %#v", completed)
	}
}

func TestInterruptActiveResponsesRetainsSavedProgress(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "recovery-user", "Recovery", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(
		ctx, user.ID, "Recovery", "gpt-test", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, assistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "recovery-request", "hello",
		"gpt-test", "high", "high", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SaveAssistantProgress(
		ctx, user.ID, assistant.ID, AssistantResult{
			Parts: []NewMessagePart{{
				Type: "text", TextContent: "saved before restart",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	affected, err := dataStore.InterruptActiveResponses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Fatalf("interrupted responses = %d", affected)
	}
	recovered, err := dataStore.MessageByID(ctx, user.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "interrupted" ||
		recovered.ErrorCode != "service_interrupted" ||
		len(recovered.Parts) != 1 ||
		recovered.Parts[0].TextContent != "saved before restart" {
		t.Fatalf("recovered response = %#v", recovered)
	}
}

func TestProgressiveSummarySettingIsNarrowAndAudited(t *testing.T) {
	dataStore := openTestStore(t)
	ctx := context.Background()
	admin, err := dataStore.CreateUserWithRole(
		ctx, "setting-admin", "Setting Admin", "hash", "admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	user, err := dataStore.CreateUser(ctx, "setting-user", "Setting User", "hash")
	if err != nil {
		t.Fatal(err)
	}

	initial, err := dataStore.ProgressiveSummarySetting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Key != ProgressiveSummarySettingKey ||
		initial.Value != ProgressiveSummaryModeAuto {
		t.Fatalf("initial setting = %#v", initial)
	}
	if _, err := dataStore.SetProgressiveSummaryMode(
		ctx, user.ID, ProgressiveSummaryModeOff,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("regular user update error = %v, want not found", err)
	}
	updated, err := dataStore.SetProgressiveSummaryMode(
		ctx, admin.ID, ProgressiveSummaryModeOff,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value != ProgressiveSummaryModeOff || updated.UpdatedBy != admin.ID {
		t.Fatalf("updated setting = %#v", updated)
	}
	if err := dataStore.RecordProgressiveSummaryRecheck(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	audit, err := dataStore.ListServiceSettingAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 2 ||
		audit[0].Action != "recheck" ||
		audit[1].Action != "update" ||
		audit[1].OldValue != ProgressiveSummaryModeAuto ||
		audit[1].NewValue != ProgressiveSummaryModeOff {
		t.Fatalf("service setting audit = %#v", audit)
	}
}

func TestSpeechSettingsDefaultToManualAndRequireAdministrator(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "speech-user", "Speech User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dataStore.CreateUserWithRole(
		ctx, "speech-admin", "Speech Admin", "hash", "admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	setting, err := dataStore.SpeechServiceSetting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Enabled || setting.Provider != "aliyun" ||
		setting.DefaultVoice != "longxiaochun" {
		t.Fatalf("initial speech setting = %#v", setting)
	}
	preference, err := dataStore.UserSpeechPreference(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preference.Mode != SpeechModeManual || preference.Speed != 1 ||
		preference.Voice != "" {
		t.Fatalf("initial speech preference = %#v", preference)
	}
	if _, err := dataStore.SetSpeechServiceSetting(
		ctx, user.ID, SpeechServiceSetting{
			Enabled: true, Provider: "aliyun", DefaultVoice: "longxiaochun",
		},
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("member setting update error = %v, want ErrNotFound", err)
	}
	updated, err := dataStore.SetSpeechServiceSetting(
		ctx, admin.ID, SpeechServiceSetting{
			Enabled: true, Provider: "aliyun", DefaultVoice: "longxiaochun",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled || updated.UpdatedBy != admin.ID {
		t.Fatalf("updated speech setting = %#v", updated)
	}
	preference, err = dataStore.SetUserSpeechPreference(
		ctx, user.ID, UserSpeechPreference{
			Mode: SpeechModeAuto, Speed: 1.25, Voice: "longxiaochun",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if preference.Mode != SpeechModeAuto || preference.Speed != 1.25 {
		t.Fatalf("updated speech preference = %#v", preference)
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
