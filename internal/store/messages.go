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

	now := time.Now().UnixMilli()
	if now <= originalCreatedAt {
		now = originalCreatedAt + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, conversation_id, user_id, role, model, reasoning_effort_requested,
			reasoning_effort_sent, status, parent_message_id, client_request_id, created_at
		)
		VALUES(?, ?, ?, 'assistant', ?, ?, NULLIF(?, ''), 'streaming', ?, ?, ?)
	`, assistantID, conversationID, userID, model, requestedEffort, sentEffort,
		parentMessageID, clientRequestID, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Message{}, nil, ErrDuplicateRequest
		}
		return Message{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations SET updated_at = ? WHERE id = ? AND user_id = ?
	`, now, conversationID, userID); err != nil {
		return Message{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, nil, err
	}

	allMessages, err := s.ListMessages(ctx, userID, conversationID)
	if err != nil {
		return Message{}, nil, err
	}
	var history []Message
	for index, message := range allMessages {
		if message.ID == parentMessageID {
			history = append([]Message(nil), allMessages[:index+1]...)
			break
		}
	}
	if history == nil {
		return Message{}, nil, errors.New("regeneration parent message is unavailable")
	}
	return Message{
		ID: assistantID, ConversationID: conversationID, Role: "assistant", Model: model,
		ReasoningEffortRequested: requestedEffort, ReasoningEffortSent: sentEffort,
		Status: "streaming", ParentMessageID: parentMessageID, CreatedAt: now, Parts: []MessagePart{},
	}, history, nil
}

func (s *Store) CompleteAssistant(ctx context.Context, userID, messageID string, result AssistantResult) (Message, error) {
	if result.Status == "" {
		result.Status = "completed"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback() }()

	completedAt := time.Now().UnixMilli()
	update, err := tx.ExecContext(ctx, `
		UPDATE messages
		SET provider_response_id = NULLIF(?, ''), status = ?, error_code = NULLIF(?, ''),
		    input_tokens = ?, output_tokens = ?, reasoning_tokens = ?, completed_at = ?
		WHERE id = ? AND user_id = ? AND role = 'assistant'
	`, result.ProviderResponseID, result.Status, result.ErrorCode,
		result.InputTokens, result.OutputTokens, result.ReasoningTokens, completedAt, messageID, userID)
	if err != nil {
		return Message{}, fmt.Errorf("complete assistant message: %w", err)
	}
	affected, _ := update.RowsAffected()
	if affected != 1 {
		return Message{}, ErrNotFound
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
		`, partID, messageID, sequence, part.Type, part.TextContent, rawJSON, part.AttachmentID, completedAt); err != nil {
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
				startedAt := completedAt - max(0, snapshot.DurationMS)
				var completed any
				if snapshot.Status == "completed" || snapshot.Status == "failed" {
					completed = completedAt
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
		`, itemID, messageID, providerSequence, item.ItemType, string(item.ReplayJSON), completedAt); err != nil {
			return Message{}, fmt.Errorf("insert provider item: %w", err)
		}
		providerSequence++
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return s.MessageByID(ctx, userID, messageID)
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
