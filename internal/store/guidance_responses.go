package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

func (s *Store) BeginGuidanceResponse(
	ctx context.Context,
	userID string,
	conversationID string,
	clientRequestID string,
	submission guidance.GuidanceSubmission,
	model string,
	requestedEffort string,
	sentEffort string,
) (Message, Message, error) {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, Message{}, fmt.Errorf("begin guidance response: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var duplicate int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages
		WHERE user_id = ? AND client_request_id = ?
	`, userID, clientRequestID).Scan(&duplicate); err != nil {
		return Message{}, Message{}, err
	}
	if duplicate > 0 {
		return Message{}, Message{}, ErrDuplicateRequest
	}
	var initialWorkbench, effectiveWorkbench string
	if err := tx.QueryRowContext(ctx, `
		SELECT u.initial_workbench,
		       COALESCE(u.workbench_preference, u.initial_workbench)
		FROM conversations c
		JOIN users u ON u.id = c.user_id
		WHERE c.id = ? AND c.user_id = ? AND c.archived_at IS NULL
		  AND u.status = 'active'
	`, conversationID, userID).Scan(&initialWorkbench, &effectiveWorkbench); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, Message{}, ErrNotFound
		}
		return Message{}, Message{}, err
	}
	if initialWorkbench != guidance.WorkbenchRestaurant ||
		effectiveWorkbench != guidance.WorkbenchRestaurant {
		return Message{}, Message{}, ErrStaleGuidance
	}

	var sourceConversationID, sourceRole, sourceStatus, sourceType, sourceJSON string
	var sourceCreatedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT m.conversation_id, m.role, m.status, m.created_at,
		       p.type, COALESCE(p.json_content, '')
		FROM messages m
		JOIN message_parts p ON p.message_id = m.id
		WHERE m.id = ? AND p.id = ? AND m.user_id = ?
	`, submission.SourceAssistantMessageID, submission.SourcePartID, userID).Scan(
		&sourceConversationID, &sourceRole, &sourceStatus, &sourceCreatedAt,
		&sourceType, &sourceJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, Message{}, err
	}
	if sourceConversationID != conversationID ||
		sourceRole != "assistant" ||
		sourceStatus != "completed" ||
		(sourceType != guidance.PartClarification && sourceType != guidance.PartTaskBrief) {
		return Message{}, Message{}, ErrStaleGuidance
	}
	var latestMessageID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM messages
		WHERE conversation_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, conversationID, userID).Scan(&latestMessageID); err != nil {
		return Message{}, Message{}, err
	}
	if latestMessageID != submission.SourceAssistantMessageID {
		return Message{}, Message{}, ErrStaleGuidance
	}

	stored, normalizedText, mutation, err := guidance.ValidateSubmission(
		sourceType,
		json.RawMessage(sourceJSON),
		submission,
	)
	if err != nil {
		return Message{}, Message{}, fmt.Errorf("validate guidance submission: %w", err)
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		return Message{}, Message{}, err
	}
	now := time.Now().UnixMilli()
	if now <= sourceCreatedAt {
		now = sourceCreatedAt + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages(
			id, conversation_id, user_id, role, status, client_request_id, created_at
		)
		VALUES(?, ?, ?, 'user', 'completed', ?, ?)
	`, userMessageID, conversationID, userID, clientRequestID, now); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Message{}, Message{}, ErrDuplicateRequest
		}
		return Message{}, Message{}, fmt.Errorf("insert guidance user message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_parts(
			id, message_id, sequence, type, text_content, json_content, created_at
		)
		VALUES(?, ?, 0, ?, ?, ?, ?)
	`, userPartID, userMessageID, guidance.PartClarificationSubmission,
		normalizedText, string(storedJSON), now); err != nil {
		return Message{}, Message{}, fmt.Errorf("insert guidance submission part: %w", err)
	}
	if mutation != nil {
		if err := applyRestaurantProfileMutation(
			ctx, tx, userID, userMessageID, *mutation,
		); err != nil {
			return Message{}, Message{}, err
		}
	}
	assistantCreatedAt, err := startAssistantTurn(ctx, tx, assistantTurn{
		UserID: userID, ConversationID: conversationID, AssistantID: assistantMessageID,
		ParentMessageID: userMessageID, Model: model,
		RequestedEffort: requestedEffort, SentEffort: sentEffort,
		NotBefore: now,
	})
	if err != nil {
		return Message{}, Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, Message{}, fmt.Errorf("commit guidance response: %w", err)
	}

	userMessage := Message{
		ID: userMessageID, ConversationID: conversationID, Role: "user",
		Status: "completed", CreatedAt: now,
		Parts: []MessagePart{{
			ID: userPartID, Sequence: 0, Type: guidance.PartClarificationSubmission,
			TextContent: normalizedText, JSONContent: storedJSON, CreatedAt: now,
		}},
	}
	assistantMessage := Message{
		ID: assistantMessageID, ConversationID: conversationID, Role: "assistant",
		Model: model, ReasoningEffortRequested: requestedEffort,
		ReasoningEffortSent: sentEffort, Status: "streaming",
		ParentMessageID: userMessageID, CreatedAt: assistantCreatedAt,
		Parts: []MessagePart{},
	}
	return userMessage, assistantMessage, nil
}
