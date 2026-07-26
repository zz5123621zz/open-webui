package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func completeTestAssistant(
	t *testing.T,
	dataStore *Store,
	userID, assistantID, text string,
	inputTokens, outputTokens int64,
) {
	t.Helper()
	_, err := dataStore.CompleteAssistant(context.Background(), userID, assistantID, AssistantResult{
		Status: "completed", InputTokens: inputTokens, OutputTokens: outputTokens,
		Parts: []NewMessagePart{{Type: "text", TextContent: text}},
	})
	if err != nil {
		t.Fatalf("CompleteAssistant() error = %v", err)
	}
}

func TestSearchConversationsScopesEscapesAndSnippets(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	alice, err := dataStore.CreateUser(ctx, "search-alice", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := dataStore.CreateUser(ctx, "search-bob", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}

	titled, err := dataStore.CreateConversation(ctx, alice.ID, "东京旅行计划", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := dataStore.CreateConversation(ctx, alice.ID, "Second chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	userMessage, assistant, err := dataStore.BeginResponse(
		ctx, alice.ID, chat.ID, "search-request-1",
		"请帮我比较 100% 全麦面包的做法", "gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = userMessage
	completeTestAssistant(t, dataStore, alice.ID, assistant.ID, "全麦面包需要更多水分。", 10, 20)

	bobChat, err := dataStore.CreateConversation(ctx, bob.ID, "东京出差", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}

	// Title match, scoped to Alice.
	results, err := dataStore.SearchConversations(ctx, alice.ID, "东京", 20)
	if err != nil {
		t.Fatalf("SearchConversations() error = %v", err)
	}
	if len(results) != 1 || results[0].Conversation.ID != titled.ID || results[0].MatchedIn != "title" {
		t.Fatalf("title search results = %#v", results)
	}

	// Message text match includes a snippet around the query.
	results, err = dataStore.SearchConversations(ctx, alice.ID, "全麦面包", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Conversation.ID != chat.ID || results[0].MatchedIn != "message" {
		t.Fatalf("message search results = %#v", results)
	}
	if !strings.Contains(results[0].Snippet, "全麦面包") {
		t.Fatalf("snippet = %q", results[0].Snippet)
	}

	// LIKE metacharacters match literally instead of acting as wildcards.
	results, err = dataStore.SearchConversations(ctx, alice.ID, "100%", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Conversation.ID != chat.ID {
		t.Fatalf("escaped search results = %#v", results)
	}
	results, err = dataStore.SearchConversations(ctx, alice.ID, "%", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("literal %% should only match the message containing it: %#v", results)
	}

	// Archived conversations disappear from search.
	if _, err := dataStore.SetConversationArchived(ctx, alice.ID, titled.ID, true); err != nil {
		t.Fatal(err)
	}
	results, err = dataStore.SearchConversations(ctx, alice.ID, "东京", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("archived conversation still matched: %#v", results)
	}

	// The administrator variant sees every user with owner attribution.
	results, err = dataStore.SearchAllConversations(ctx, "东京", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Conversation.ID != bobChat.ID ||
		results[0].Conversation.OwnerUsername != "search-bob" {
		t.Fatalf("admin search results = %#v", results)
	}
}

func TestBeginEditValidatesAndRewritesLatestUserMessage(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "edit-user", "Edit User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Edit chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	firstUser, firstAssistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "edit-request-1", "first question", "gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Editing is rejected while a response is still streaming.
	if _, _, err := dataStore.BeginEdit(
		ctx, user.ID, firstUser.ID, "edit-request-2", "changed", "gpt-test", "auto", "",
	); err == nil {
		t.Fatal("BeginEdit() during streaming response should fail")
	}
	completeTestAssistant(t, dataStore, user.ID, firstAssistant.ID, "first answer", 5, 9)

	// Editing an assistant message is rejected.
	if _, _, err := dataStore.BeginEdit(
		ctx, user.ID, firstAssistant.ID, "edit-request-3", "changed", "gpt-test", "auto", "",
	); err == nil {
		t.Fatal("BeginEdit() on an assistant message should fail")
	}

	secondUser, secondAssistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "edit-request-4", "second question", "gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	completeTestAssistant(t, dataStore, user.ID, secondAssistant.ID, "second answer", 6, 10)

	// Only the latest user message can be edited.
	if _, _, err := dataStore.BeginEdit(
		ctx, user.ID, firstUser.ID, "edit-request-5", "changed", "gpt-test", "auto", "",
	); !errors.Is(err, ErrNotLatestMessage) {
		t.Fatalf("BeginEdit() on older message error = %v, want ErrNotLatestMessage", err)
	}

	// Blank replacements are rejected for a text-only message.
	if _, _, err := dataStore.BeginEdit(
		ctx, user.ID, secondUser.ID, "edit-request-6", "   ", "gpt-test", "auto", "",
	); err == nil {
		t.Fatal("BeginEdit() with empty text should fail for a text-only message")
	}

	assistant, history, err := dataStore.BeginEdit(
		ctx, user.ID, secondUser.ID, "edit-request-7", "rewritten question", "gpt-test", "high", "high",
	)
	if err != nil {
		t.Fatalf("BeginEdit() error = %v", err)
	}
	if assistant.ParentMessageID != secondUser.ID || assistant.Status != "streaming" {
		t.Fatalf("edited assistant = %#v", assistant)
	}
	last := history[len(history)-1]
	if last.ID != secondUser.ID || len(last.Parts) != 1 || last.Parts[0].TextContent != "rewritten question" {
		t.Fatalf("edit history tail = %#v", last)
	}

	// The transcript keeps the earlier answer and appends the new sibling last.
	completeTestAssistant(t, dataStore, user.ID, assistant.ID, "rewritten answer", 7, 11)
	messages, err := dataStore.ListMessages(ctx, user.ID, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 5 {
		t.Fatalf("message count after edit = %d", len(messages))
	}
	tail := messages[len(messages)-1]
	if tail.ID != assistant.ID || tail.ParentMessageID != secondUser.ID {
		t.Fatalf("transcript tail = %#v", tail)
	}

	// Duplicate request IDs are rejected.
	if _, _, err := dataStore.BeginEdit(
		ctx, user.ID, secondUser.ID, "edit-request-7", "again", "gpt-test", "auto", "",
	); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate edit error = %v, want ErrDuplicateRequest", err)
	}
}

func TestBeginEditAddsTextToImageOnlyMessage(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "edit-image-user", "Edit Image User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(ctx, user.ID, "Image chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"edit-image-1", "edit-image-2"} {
		if _, err := dataStore.CreateAttachment(ctx, Attachment{
			ID: id, UserID: user.ID, ConversationID: conversation.ID,
			Kind: "upload", MediaType: "image/png", ByteSize: 10,
			SHA256: "hash-" + id, StoragePath: "uploads/" + id + ".png",
		}); err != nil {
			t.Fatal(err)
		}
	}
	userMessage, assistant, err := dataStore.BeginResponse(
		ctx, user.ID, conversation.ID, "edit-image-request-1", "",
		"gpt-test", "auto", "", []string{"edit-image-1", "edit-image-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	completeTestAssistant(t, dataStore, user.ID, assistant.ID, "image answer", 4, 6)

	// Regression: adding text to a message that has 2+ image parts must not
	// trip UNIQUE(message_id, sequence).
	_, history, err := dataStore.BeginEdit(
		ctx, user.ID, userMessage.ID, "edit-image-request-2",
		"what is in these images?", "gpt-test", "auto", "",
	)
	if err != nil {
		t.Fatalf("BeginEdit() on image-only message error = %v", err)
	}
	edited := history[len(history)-1]
	if len(edited.Parts) != 3 {
		t.Fatalf("edited parts = %#v", edited.Parts)
	}
	if edited.Parts[0].Type != "text" || edited.Parts[0].TextContent != "what is in these images?" {
		t.Fatalf("edited first part = %#v", edited.Parts[0])
	}
	if edited.Parts[1].Type != "image" || edited.Parts[2].Type != "image" {
		t.Fatalf("edited image parts = %#v", edited.Parts[1:])
	}
}

func TestSearchIsNotStarvedByOneChattyConversation(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "starve-user", "Starve User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	older, err := dataStore.CreateConversation(ctx, user.ID, "Older chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	_, olderAssistant, err := dataStore.BeginResponse(
		ctx, user.ID, older.ID, "starve-old", "keyword in the old chat", "gpt-test", "auto", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	completeTestAssistant(t, dataStore, user.ID, olderAssistant.ID, "old answer", 1, 1)

	chatty, err := dataStore.CreateConversation(ctx, user.ID, "Chatty chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 6; round++ {
		_, assistant, err := dataStore.BeginResponse(
			ctx, user.ID, chatty.ID, fmt.Sprintf("starve-%d", round),
			fmt.Sprintf("keyword repeated %d", round), "gpt-test", "auto", "", nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		completeTestAssistant(
			t, dataStore, user.ID, assistant.ID,
			fmt.Sprintf("keyword answered %d", round), 1, 1,
		)
	}

	// A tiny limit forces the dedup to happen inside SQL: the chatty
	// conversation must collapse to one row instead of consuming the window.
	results, err := dataStore.SearchConversations(ctx, user.ID, "keyword", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("starvation results = %#v", results)
	}
	// Millisecond-resolution updated_at can tie in a fast test, which makes
	// the relative order fall back to random ids; only membership is asserted.
	found := map[string]bool{}
	for _, result := range results {
		found[result.Conversation.ID] = true
	}
	if !found[chatty.ID] || !found[older.ID] {
		t.Fatalf("starvation membership = %#v (chatty=%s older=%s)", results, chatty.ID, older.ID)
	}
}

func TestUsageByMonthAggregatesCompletedResponses(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	alice, err := dataStore.CreateUser(ctx, "usage-alice", "Alice", "hash")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := dataStore.CreateUser(ctx, "usage-bob", "Bob", "hash")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := dataStore.CreateConversation(ctx, alice.ID, "Usage chat", "gpt-test", "auto")
	if err != nil {
		t.Fatal(err)
	}
	for index, tokens := range []int64{100, 200} {
		_, assistant, err := dataStore.BeginResponse(
			ctx, alice.ID, chat.ID, "usage-request-"+string(rune('a'+index)),
			"question", "gpt-test", "auto", "", nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		completeTestAssistant(t, dataStore, alice.ID, assistant.ID, "answer", tokens, tokens*2)
	}
	// A still-streaming response is excluded from usage.
	if _, _, err := dataStore.BeginResponse(
		ctx, alice.ID, chat.ID, "usage-request-open", "question", "gpt-test", "auto", "", nil,
	); err != nil {
		t.Fatal(err)
	}

	rows, err := dataStore.UsageByMonth(ctx, alice.ID, 6)
	if err != nil {
		t.Fatalf("UsageByMonth() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("usage rows = %#v", rows)
	}
	row := rows[0]
	if row.Model != "gpt-test" || row.Responses != 2 ||
		row.InputTokens != 300 || row.OutputTokens != 600 {
		t.Fatalf("usage row = %#v", row)
	}
	if len(row.Month) != 7 || row.Month[4] != '-' {
		t.Fatalf("usage month = %q", row.Month)
	}

	// Bob has no completed responses and therefore no rows.
	rows, err = dataStore.UsageByMonth(ctx, bob.ID, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("bob usage rows = %#v", rows)
	}

	// The administrator variant attributes rows to their owners.
	rows, err = dataStore.UsageByMonthAllUsers(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].OwnerUsername != "usage-alice" {
		t.Fatalf("admin usage rows = %#v", rows)
	}
}
