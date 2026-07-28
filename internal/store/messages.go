package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type Message struct {
	ID                       string         `json:"id"`
	ConversationID           string         `json:"conversationId"`
	Role                     string         `json:"role"`
	Model                    string         `json:"model,omitempty"`
	ReasoningEffortRequested string         `json:"reasoningEffortRequested,omitempty"`
	ReasoningEffortSent      string         `json:"reasoningEffortSent,omitempty"`
	Status                   string         `json:"status"`
	ParentMessageID          string         `json:"parentMessageId,omitempty"`
	ProviderResponseID       string         `json:"providerResponseId,omitempty"`
	InputTokens              int64          `json:"inputTokens,omitempty"`
	OutputTokens             int64          `json:"outputTokens,omitempty"`
	ReasoningTokens          int64          `json:"reasoningTokens,omitempty"`
	ErrorCode                string         `json:"errorCode,omitempty"`
	CreatedAt                int64          `json:"createdAt"`
	CompletedAt              int64          `json:"completedAt,omitempty"`
	Parts                    []MessagePart  `json:"parts"`
	ProviderItems            []ProviderItem `json:"-"`
}

type MessagePart struct {
	ID           string          `json:"id"`
	Sequence     int             `json:"sequence"`
	Type         string          `json:"type"`
	TextContent  string          `json:"text,omitempty"`
	JSONContent  json.RawMessage `json:"data,omitempty"`
	AttachmentID string          `json:"attachmentId,omitempty"`
	CreatedAt    int64           `json:"createdAt"`
}

type AssistantResult struct {
	ProviderResponseID string
	Status             string
	ErrorCode          string
	InputTokens        int64
	OutputTokens       int64
	ReasoningTokens    int64
	Parts              []NewMessagePart
	ProviderItems      []NewProviderItem
}

type NewMessagePart struct {
	Type         string
	TextContent  string
	JSONContent  json.RawMessage
	AttachmentID string
}

type ProviderItem struct {
	Sequence   int
	ItemType   string
	ReplayJSON json.RawMessage
}

type NewProviderItem struct {
	ItemType   string
	ReplayJSON json.RawMessage
}

func (s *Store) BeginResponse(ctx context.Context, userID, conversationID, clientRequestID, text, model, requestedEffort, sentEffort string, attachmentIDs []string) (Message, Message, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(attachmentIDs) == 0 {
		return Message{}, Message{}, errors.New("message content is required")
	}
	userMessageID, err := ids.New()
	if err != nil {
		return Message{}, Message{}, err
	}
	userPartID, err := ids.New()
	if err != nil {
		return Message{}, Message{}, err
	}
	assistantMessageID, err := ids.New()
	if err != nil {
		return Message{}, Message{}, err
	}
	now := time.Now().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Message{}, fmt.Errorf("begin response: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM conversations WHERE id = ? AND user_id = ? AND archived_at IS NULL`, conversationID, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, Message{}, ErrNotFound
		}
		return Message{}, Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(id, conversation_id, user_id, role, status, client_request_id, created_at)
		VALUES(?, ?, ?, 'user', 'completed', ?, ?)
	`, userMessageID, conversationID, userID, clientRequestID, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Message{}, Message{}, ErrDuplicateRequest
		}
		return Message{}, Message{}, fmt.Errorf("insert user message: %w", err)
	}
	sequence := 0
	userParts := make([]MessagePart, 0, 1+len(attachmentIDs))
	if text != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts(id, message_id, sequence, type, text_content, created_at)
			VALUES(?, ?, ?, 'text', ?, ?)
		`, userPartID, userMessageID, sequence, text, now); err != nil {
			return Message{}, Message{}, fmt.Errorf("insert user text: %w", err)
		}
		userParts = append(userParts, MessagePart{
			ID: userPartID, Sequence: sequence, Type: "text", TextContent: text, CreatedAt: now,
		})
		sequence++
	}
	if err := s.BindAttachments(ctx, tx, userID, conversationID, userMessageID, attachmentIDs); err != nil {
		return Message{}, Message{}, fmt.Errorf("bind attachments: %w", err)
	}
	for _, attachmentID := range attachmentIDs {
		partID, idErr := ids.New()
		if idErr != nil {
			return Message{}, Message{}, idErr
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts(id, message_id, sequence, type, attachment_id, created_at)
			VALUES(?, ?, ?, 'image', ?, ?)
		`, partID, userMessageID, sequence, attachmentID, now); err != nil {
			return Message{}, Message{}, fmt.Errorf("insert user image: %w", err)
		}
		userParts = append(userParts, MessagePart{
			ID: partID, Sequence: sequence, Type: "image", AttachmentID: attachmentID, CreatedAt: now,
		})
		sequence++
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, conversation_id, user_id, role, model, reasoning_effort_requested,
			reasoning_effort_sent, status, parent_message_id, created_at
		)
		VALUES(?, ?, ?, 'assistant', ?, ?, NULLIF(?, ''), 'streaming', ?, ?)
	`, assistantMessageID, conversationID, userID, model, requestedEffort, sentEffort, userMessageID, now+1); err != nil {
		return Message{}, Message{}, fmt.Errorf("insert assistant message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ? AND user_id = ?`, now, conversationID, userID); err != nil {
		return Message{}, Message{}, fmt.Errorf("touch conversation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Message{}, Message{}, fmt.Errorf("commit response start: %w", err)
	}

	userMessage := Message{
		ID: userMessageID, ConversationID: conversationID, Role: "user", Status: "completed",
		CreatedAt: now, Parts: userParts,
	}
	assistantMessage := Message{
		ID: assistantMessageID, ConversationID: conversationID, Role: "assistant", Model: model,
		ReasoningEffortRequested: requestedEffort, ReasoningEffortSent: sentEffort,
		Status: "streaming", ParentMessageID: userMessageID, CreatedAt: now + 1, Parts: []MessagePart{},
	}
	return userMessage, assistantMessage, nil
}

func (s *Store) BeginRegeneration(
	ctx context.Context,
	userID string,
	originalAssistantID string,
	clientRequestID string,
	model string,
	requestedEffort string,
	sentEffort string,
) (Message, []Message, error) {
	assistantID, err := ids.New()
	if err != nil {
		return Message{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var conversationID, parentMessageID, role, status string
	var originalCreatedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT conversation_id, COALESCE(parent_message_id, ''), role, status, created_at
		FROM messages
		WHERE id = ? AND user_id = ?
	`, originalAssistantID, userID).Scan(
		&conversationID, &parentMessageID, &role, &status, &originalCreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, nil, ErrNotFound
	}
	if err != nil {
		return Message{}, nil, err
	}
	if role != "assistant" || parentMessageID == "" {
		return Message{}, nil, errors.New("only an assistant response can be regenerated")
	}
	if status == "streaming" || status == "pending" {
		return Message{}, nil, errors.New("response is still running")
	}
	var latestID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM messages
		WHERE conversation_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, conversationID, userID).Scan(&latestID); err != nil {
		return Message{}, nil, err
	}
	if latestID != originalAssistantID {
		return Message{}, nil, ErrNotLatestMessage
	}

	now, err := startAssistantTurn(ctx, tx, assistantTurn{
		UserID: userID, ConversationID: conversationID, AssistantID: assistantID,
		ParentMessageID: parentMessageID, ClientRequestID: clientRequestID,
		Model: model, RequestedEffort: requestedEffort, SentEffort: sentEffort,
		NotBefore: originalCreatedAt,
	})
	if err != nil {
		return Message{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, nil, err
	}

	allMessages, err := s.ListMessages(ctx, userID, conversationID)
	if err != nil {
		return Message{}, nil, err
	}
	history := historyThrough(allMessages, parentMessageID)
	if history == nil {
		return Message{}, nil, errors.New("regeneration parent message is unavailable")
	}
	return Message{
		ID: assistantID, ConversationID: conversationID, Role: "assistant", Model: model,
		ReasoningEffortRequested: requestedEffort, ReasoningEffortSent: sentEffort,
		Status: "streaming", ParentMessageID: parentMessageID, CreatedAt: now, Parts: []MessagePart{},
	}, history, nil
}

type assistantTurn struct {
	UserID          string
	ConversationID  string
	AssistantID     string
	ParentMessageID string
	ClientRequestID string
	Model           string
	RequestedEffort string
	SentEffort      string
	NotBefore       int64
}

// startAssistantTurn inserts a streaming assistant message that sorts after
// every existing message and touches the conversation timestamp. It maps a
// duplicate client request id onto ErrDuplicateRequest.
func startAssistantTurn(ctx context.Context, tx *sql.Tx, turn assistantTurn) (int64, error) {
	now := time.Now().UnixMilli()
	if now <= turn.NotBefore {
		now = turn.NotBefore + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, conversation_id, user_id, role, model, reasoning_effort_requested,
			reasoning_effort_sent, status, parent_message_id, client_request_id, created_at
		)
		VALUES(
			?, ?, ?, 'assistant', ?, ?, NULLIF(?, ''), 'streaming', ?,
			NULLIF(?, ''), ?
		)
	`, turn.AssistantID, turn.ConversationID, turn.UserID, turn.Model, turn.RequestedEffort,
		turn.SentEffort, turn.ParentMessageID, turn.ClientRequestID, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, ErrDuplicateRequest
		}
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations SET updated_at = ? WHERE id = ? AND user_id = ?
	`, now, turn.ConversationID, turn.UserID); err != nil {
		return 0, err
	}
	return now, nil
}

// historyThrough returns a copy of the prefix of messages up to and including
// the boundary message, or nil when the boundary is absent.
func historyThrough(messages []Message, boundaryID string) []Message {
	for index, message := range messages {
		if message.ID == boundaryID {
			return append([]Message(nil), messages[:index+1]...)
		}
	}
	return nil
}

// BeginEdit rewrites the text of the latest user message and starts a fresh
// assistant response for it. Earlier assistant answers to the same message
// stay in the transcript, exactly like a regeneration.
func (s *Store) BeginEdit(
	ctx context.Context,
	userID string,
	userMessageID string,
	clientRequestID string,
	newText string,
	model string,
	requestedEffort string,
	sentEffort string,
) (Message, []Message, error) {
	newText = strings.TrimSpace(newText)
	assistantID, err := ids.New()
	if err != nil {
		return Message{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var conversationID, role string
	err = tx.QueryRowContext(ctx, `
		SELECT conversation_id, role FROM messages WHERE id = ? AND user_id = ?
	`, userMessageID, userID).Scan(&conversationID, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, nil, ErrNotFound
	}
	if err != nil {
		return Message{}, nil, err
	}
	if role != "user" {
		return Message{}, nil, errors.New("only a user message can be edited")
	}
	var archivedExists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM conversations WHERE id = ? AND user_id = ? AND archived_at IS NULL
	`, conversationID, userID).Scan(&archivedExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, nil, ErrNotFound
		}
		return Message{}, nil, err
	}
	var latestUserID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM messages
		WHERE conversation_id = ? AND user_id = ? AND role = 'user'
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, conversationID, userID).Scan(&latestUserID); err != nil {
		return Message{}, nil, err
	}
	if latestUserID != userMessageID {
		return Message{}, nil, ErrNotLatestMessage
	}
	var running int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages
		WHERE conversation_id = ? AND status IN ('pending', 'streaming')
	`, conversationID).Scan(&running); err != nil {
		return Message{}, nil, err
	}
	if running > 0 {
		return Message{}, nil, errors.New("response is still running")
	}

	var textPartID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM message_parts
		WHERE message_id = ? AND type = 'text'
		ORDER BY sequence LIMIT 1
	`, userMessageID).Scan(&textPartID)
	hasTextPart := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Message{}, nil, err
	}
	if newText == "" {
		var imageParts int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM message_parts WHERE message_id = ? AND type = 'image'
		`, userMessageID).Scan(&imageParts); err != nil {
			return Message{}, nil, err
		}
		if imageParts == 0 {
			return Message{}, nil, errors.New("message content is required")
		}
	}
	switch {
	case hasTextPart && newText != "":
		if _, err := tx.ExecContext(ctx, `
			UPDATE message_parts SET text_content = ? WHERE id = ?
		`, newText, textPartID); err != nil {
			return Message{}, nil, fmt.Errorf("update edited text: %w", err)
		}
	case hasTextPart:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM message_parts WHERE id = ?
		`, textPartID); err != nil {
			return Message{}, nil, fmt.Errorf("remove edited text: %w", err)
		}
	case newText != "":
		partID, idErr := ids.New()
		if idErr != nil {
			return Message{}, nil, idErr
		}
		// Insert below the smallest existing sequence instead of shifting the
		// other parts up: a bulk +1 shift trips UNIQUE(message_id, sequence)
		// as soon as the message has two parts.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts(id, message_id, sequence, type, text_content, created_at)
			VALUES(
				?, ?,
				(SELECT COALESCE(MIN(sequence), 1) - 1 FROM message_parts WHERE message_id = ?),
				'text', ?, ?
			)
		`, partID, userMessageID, userMessageID, newText, time.Now().UnixMilli()); err != nil {
			return Message{}, nil, fmt.Errorf("insert edited text: %w", err)
		}
	}

	var latestCreatedAt int64
	if err := tx.QueryRowContext(ctx, `
		SELECT created_at FROM messages
		WHERE conversation_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, conversationID, userID).Scan(&latestCreatedAt); err != nil {
		return Message{}, nil, err
	}
	now, err := startAssistantTurn(ctx, tx, assistantTurn{
		UserID: userID, ConversationID: conversationID, AssistantID: assistantID,
		ParentMessageID: userMessageID, ClientRequestID: clientRequestID,
		Model: model, RequestedEffort: requestedEffort, SentEffort: sentEffort,
		NotBefore: latestCreatedAt,
	})
	if err != nil {
		return Message{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, nil, err
	}

	allMessages, err := s.ListMessages(ctx, userID, conversationID)
	if err != nil {
		return Message{}, nil, err
	}
	history := historyThrough(allMessages, userMessageID)
	if history == nil {
		return Message{}, nil, errors.New("edited message is unavailable")
	}
	return Message{
		ID: assistantID, ConversationID: conversationID, Role: "assistant", Model: model,
		ReasoningEffortRequested: requestedEffort, ReasoningEffortSent: sentEffort,
		Status: "streaming", ParentMessageID: userMessageID, CreatedAt: now, Parts: []MessagePart{},
	}, history, nil
}

func (s *Store) SaveAssistantProgress(ctx context.Context, userID, messageID string, result AssistantResult) (Message, error) {
	result.Status = "streaming"
	result.ErrorCode = ""
	return s.replaceAssistantResult(ctx, userID, messageID, result, false)
}

func (s *Store) CompleteAssistant(ctx context.Context, userID, messageID string, result AssistantResult) (Message, error) {
	if result.Status == "" {
		result.Status = "completed"
	}
	return s.replaceAssistantResult(ctx, userID, messageID, result, true)
}

func (s *Store) replaceAssistantResult(
	ctx context.Context,
	userID string,
	messageID string,
	result AssistantResult,
	final bool,
) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback() }()

	savedAt := time.Now().UnixMilli()
	var update sql.Result
	if final {
		update, err = tx.ExecContext(ctx, `
			UPDATE messages
			SET provider_response_id = NULLIF(?, ''), status = ?, error_code = NULLIF(?, ''),
			    input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, completed_at = ?
			WHERE id = ? AND user_id = ? AND role = 'assistant'
		`, result.ProviderResponseID, result.Status, result.ErrorCode,
			result.InputTokens, result.OutputTokens, result.ReasoningTokens, savedAt, messageID, userID)
	} else {
		update, err = tx.ExecContext(ctx, `
			UPDATE messages
			SET provider_response_id = NULLIF(?, ''), status = 'streaming', error_code = NULL,
			    input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, completed_at = NULL
			WHERE id = ? AND user_id = ? AND role = 'assistant'
			  AND status IN ('pending', 'streaming')
		`, result.ProviderResponseID, result.InputTokens, result.OutputTokens,
			result.ReasoningTokens, messageID, userID)
	}
	if err != nil {
		return Message{}, fmt.Errorf("save assistant message: %w", err)
	}
	affected, _ := update.RowsAffected()
	if affected != 1 {
		return Message{}, ErrNotFound
	}
	for _, table := range []string{"message_parts", "tool_events", "provider_items"} {
		if _, err := tx.ExecContext(
			ctx, "DELETE FROM "+table+" WHERE message_id = ?", messageID,
		); err != nil {
			return Message{}, fmt.Errorf("replace assistant %s: %w", table, err)
		}
	}
	for sequence, part := range result.Parts {
		partID, idErr := ids.New()
		if idErr != nil {
			return Message{}, idErr
		}
		var rawJSON any
		if len(part.JSONContent) > 0 {
			rawJSON = string(part.JSONContent)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_parts(id, message_id, sequence, type, text_content, json_content, attachment_id, created_at)
			VALUES(?, ?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?)
		`, partID, messageID, sequence, part.Type, part.TextContent, rawJSON, part.AttachmentID, savedAt); err != nil {
			return Message{}, fmt.Errorf("insert assistant part: %w", err)
		}
		if part.Type == "tool" && len(part.JSONContent) > 0 {
			var snapshot struct {
				CallID     string          `json:"callId"`
				Type       string          `json:"type"`
				Status     string          `json:"status"`
				Data       json.RawMessage `json:"data"`
				DurationMS int64           `json:"durationMs"`
				ErrorCode  string          `json:"errorCode"`
			}
			if json.Unmarshal(part.JSONContent, &snapshot) == nil && snapshot.Type != "" {
				eventID, idErr := ids.New()
				if idErr != nil {
					return Message{}, idErr
				}
				startedAt := savedAt - max(0, snapshot.DurationMS)
				var completed any
				if snapshot.Status == "completed" || snapshot.Status == "failed" {
					completed = savedAt
				}
				var safeResult string
				if snapshot.ErrorCode != "" {
					raw, _ := json.Marshal(map[string]string{"errorCode": snapshot.ErrorCode})
					safeResult = string(raw)
				}
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO tool_events(
						id, message_id, call_id, tool_type, status, safe_arguments_json,
						safe_result_json, started_at, completed_at
					)
					VALUES(?, ?, NULLIF(?, ''), ?, ?, ?, NULLIF(?, ''), ?, ?)
				`, eventID, messageID, snapshot.CallID, snapshot.Type, snapshot.Status,
					string(snapshot.Data), safeResult, startedAt, completed); err != nil {
					return Message{}, fmt.Errorf("insert tool event: %w", err)
				}
			}
		}
	}
	providerSequence := 0
	for _, item := range result.ProviderItems {
		// Reasoning provider items can contain opaque encrypted chain-of-thought.
		// Safe, user-visible summaries are persisted as message parts instead.
		if item.ItemType == "reasoning" {
			continue
		}
		if item.ItemType == "" || !json.Valid(item.ReplayJSON) {
			return Message{}, errors.New("invalid provider replay item")
		}
		if item.ItemType != "message" && item.ItemType != "web_search_call" {
			return Message{}, errors.New("unsupported provider replay item")
		}
		itemID, idErr := ids.New()
		if idErr != nil {
			return Message{}, idErr
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_items(id, message_id, sequence, item_type, replay_json, created_at)
			VALUES(?, ?, ?, ?, ?, ?)
		`, itemID, messageID, providerSequence, item.ItemType, string(item.ReplayJSON), savedAt); err != nil {
			return Message{}, fmt.Errorf("insert provider item: %w", err)
		}
		providerSequence++
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	if !final {
		return Message{}, nil
	}
	return s.MessageByID(ctx, userID, messageID)
}

func (s *Store) InterruptActiveResponses(ctx context.Context) (int64, error) {
	completedAt := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `
		UPDATE messages
		SET status = 'interrupted', error_code = 'service_interrupted', completed_at = ?
		WHERE role = 'assistant' AND status IN ('pending', 'streaming')
	`, completedAt)
	if err != nil {
		return 0, fmt.Errorf("interrupt active responses: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count interrupted responses: %w", err)
	}
	return affected, nil
}

func (s *Store) ListMessages(ctx context.Context, userID, conversationID string) ([]Message, error) {
	if _, err := s.OwnedConversationByID(ctx, userID, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, role, COALESCE(model, ''), COALESCE(reasoning_effort_requested, ''),
		       COALESCE(reasoning_effort_sent, ''), status, COALESCE(parent_message_id, ''),
		       COALESCE(provider_response_id, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		       COALESCE(reasoning_tokens, 0), COALESCE(error_code, ''), created_at, COALESCE(completed_at, 0)
		FROM messages
		WHERE conversation_id = ? AND user_id = ?
		ORDER BY created_at, id
	`, conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()

	messages := make([]Message, 0)
	for rows.Next() {
		var message Message
		if err := scanMessage(rows, &message); err != nil {
			return nil, err
		}
		parts, err := s.listMessageParts(ctx, userID, message.ID)
		if err != nil {
			return nil, err
		}
		message.Parts = parts
		items, err := s.listProviderItems(ctx, userID, message.ID)
		if err != nil {
			return nil, err
		}
		message.ProviderItems = items
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) MessageByID(ctx context.Context, userID, messageID string) (Message, error) {
	var message Message
	row := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, role, COALESCE(model, ''), COALESCE(reasoning_effort_requested, ''),
		       COALESCE(reasoning_effort_sent, ''), status, COALESCE(parent_message_id, ''),
		       COALESCE(provider_response_id, ''), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		       COALESCE(reasoning_tokens, 0), COALESCE(error_code, ''), created_at, COALESCE(completed_at, 0)
		FROM messages WHERE id = ? AND user_id = ?
	`, messageID, userID)
	if err := scanMessage(row, &message); errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	} else if err != nil {
		return Message{}, err
	}
	parts, err := s.listMessageParts(ctx, userID, message.ID)
	if err != nil {
		return Message{}, err
	}
	message.Parts = parts
	items, err := s.listProviderItems(ctx, userID, message.ID)
	if err != nil {
		return Message{}, err
	}
	message.ProviderItems = items
	return message, nil
}

// LatestAssistantChild returns the most recent assistant response whose
// parent is the given message.
func (s *Store) LatestAssistantChild(ctx context.Context, userID, parentMessageID string) (Message, error) {
	var childID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM messages
		WHERE parent_message_id = ? AND user_id = ? AND role = 'assistant'
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, parentMessageID, userID).Scan(&childID)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	return s.MessageByID(ctx, userID, childID)
}

type scanner interface {
	Scan(...any) error
}

func scanMessage(row scanner, message *Message) error {
	return row.Scan(
		&message.ID, &message.ConversationID, &message.Role, &message.Model,
		&message.ReasoningEffortRequested, &message.ReasoningEffortSent, &message.Status,
		&message.ParentMessageID, &message.ProviderResponseID, &message.InputTokens,
		&message.OutputTokens, &message.ReasoningTokens, &message.ErrorCode,
		&message.CreatedAt, &message.CompletedAt,
	)
}

func (s *Store) listMessageParts(ctx context.Context, userID, messageID string) ([]MessagePart, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.sequence, p.type, COALESCE(p.text_content, ''),
		       COALESCE(p.json_content, ''), COALESCE(p.attachment_id, ''), p.created_at
		FROM message_parts p
		JOIN messages m ON m.id = p.message_id
		WHERE p.message_id = ? AND m.user_id = ?
		ORDER BY p.sequence
	`, messageID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	parts := make([]MessagePart, 0)
	for rows.Next() {
		var part MessagePart
		var rawJSON string
		if err := rows.Scan(&part.ID, &part.Sequence, &part.Type, &part.TextContent, &rawJSON, &part.AttachmentID, &part.CreatedAt); err != nil {
			return nil, err
		}
		if rawJSON != "" {
			part.JSONContent = json.RawMessage(rawJSON)
		}
		parts = append(parts, part)
	}
	return parts, rows.Err()
}

func (s *Store) listProviderItems(ctx context.Context, userID, messageID string) ([]ProviderItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.sequence, p.item_type, p.replay_json
		FROM provider_items p
		JOIN messages m ON m.id = p.message_id
		WHERE p.message_id = ? AND m.user_id = ?
		ORDER BY p.sequence
	`, messageID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ProviderItem, 0)
	for rows.Next() {
		var item ProviderItem
		var raw string
		if err := rows.Scan(&item.Sequence, &item.ItemType, &raw); err != nil {
			return nil, err
		}
		item.ReplayJSON = json.RawMessage(raw)
		items = append(items, item)
	}
	return items, rows.Err()
}
