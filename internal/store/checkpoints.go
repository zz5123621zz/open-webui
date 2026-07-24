package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type ContextCheckpoint struct {
	ID                    string `json:"id"`
	ConversationID        string `json:"conversationId"`
	BoundaryMessageID     string `json:"boundaryMessageId"`
	PreviousCheckpointID  string `json:"previousCheckpointId,omitempty"`
	Model                 string `json:"model"`
	SummaryText           string `json:"summaryText"`
	SourceFirstMessageID  string `json:"sourceFirstMessageId"`
	SourceLastMessageID   string `json:"sourceLastMessageId"`
	EstimatedTokensBefore int    `json:"estimatedTokensBefore"`
	EstimatedTokensAfter  int    `json:"estimatedTokensAfter"`
	SourceBytes           int64  `json:"sourceBytes"`
	InputTokens           int64  `json:"inputTokens,omitempty"`
	OutputTokens          int64  `json:"outputTokens,omitempty"`
	Status                string `json:"status"`
	CreatedAt             int64  `json:"createdAt"`
	ExpectedHeadMessageID string `json:"-"`
}

func (s *Store) LatestCheckpoint(ctx context.Context, userID, conversationID string) (ContextCheckpoint, error) {
	var checkpoint ContextCheckpoint
	err := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, boundary_message_id, COALESCE(previous_checkpoint_id, ''),
		       model, summary_text, source_first_message_id, source_last_message_id,
		       estimated_tokens_before, estimated_tokens_after, source_bytes,
		       input_tokens, output_tokens, status, created_at
		FROM context_checkpoints
		WHERE conversation_id = ? AND user_id = ? AND status = 'completed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, conversationID, userID).Scan(
		&checkpoint.ID, &checkpoint.ConversationID, &checkpoint.BoundaryMessageID,
		&checkpoint.PreviousCheckpointID, &checkpoint.Model, &checkpoint.SummaryText,
		&checkpoint.SourceFirstMessageID, &checkpoint.SourceLastMessageID,
		&checkpoint.EstimatedTokensBefore, &checkpoint.EstimatedTokensAfter,
		&checkpoint.SourceBytes, &checkpoint.InputTokens, &checkpoint.OutputTokens,
		&checkpoint.Status, &checkpoint.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextCheckpoint{}, ErrNotFound
	}
	if err != nil {
		return ContextCheckpoint{}, fmt.Errorf("lookup context checkpoint: %w", err)
	}
	return checkpoint, nil
}

func (s *Store) ListCheckpoints(ctx context.Context, userID, conversationID string) ([]ContextCheckpoint, error) {
	if _, err := s.OwnedConversationByID(ctx, userID, conversationID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, boundary_message_id, COALESCE(previous_checkpoint_id, ''),
		       model, summary_text, source_first_message_id, source_last_message_id,
		       estimated_tokens_before, estimated_tokens_after, source_bytes,
		       input_tokens, output_tokens, status, created_at
		FROM context_checkpoints
		WHERE conversation_id = ? AND user_id = ?
		ORDER BY created_at DESC, id DESC
	`, conversationID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ContextCheckpoint, 0)
	for rows.Next() {
		var checkpoint ContextCheckpoint
		if err := rows.Scan(
			&checkpoint.ID, &checkpoint.ConversationID, &checkpoint.BoundaryMessageID,
			&checkpoint.PreviousCheckpointID, &checkpoint.Model, &checkpoint.SummaryText,
			&checkpoint.SourceFirstMessageID, &checkpoint.SourceLastMessageID,
			&checkpoint.EstimatedTokensBefore, &checkpoint.EstimatedTokensAfter,
			&checkpoint.SourceBytes, &checkpoint.InputTokens, &checkpoint.OutputTokens,
			&checkpoint.Status, &checkpoint.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, checkpoint)
	}
	return result, rows.Err()
}

func (s *Store) CreateCheckpoint(ctx context.Context, userID string, checkpoint ContextCheckpoint) (ContextCheckpoint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ContextCheckpoint{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var existing ContextCheckpoint
	err = tx.QueryRowContext(ctx, `
		SELECT id, conversation_id, boundary_message_id, COALESCE(previous_checkpoint_id, ''),
		       model, summary_text, source_first_message_id, source_last_message_id,
		       estimated_tokens_before, estimated_tokens_after, source_bytes,
		       input_tokens, output_tokens, status, created_at
		FROM context_checkpoints
		WHERE conversation_id = ? AND user_id = ? AND boundary_message_id = ?
		LIMIT 1
	`, checkpoint.ConversationID, userID, checkpoint.BoundaryMessageID).Scan(
		&existing.ID, &existing.ConversationID, &existing.BoundaryMessageID,
		&existing.PreviousCheckpointID, &existing.Model, &existing.SummaryText,
		&existing.SourceFirstMessageID, &existing.SourceLastMessageID,
		&existing.EstimatedTokensBefore, &existing.EstimatedTokensAfter,
		&existing.SourceBytes, &existing.InputTokens, &existing.OutputTokens,
		&existing.Status, &existing.CreatedAt,
	)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ContextCheckpoint{}, err
	}
	if checkpoint.ExpectedHeadMessageID != "" {
		var currentHead string
		err = tx.QueryRowContext(ctx, `
			SELECT id
			FROM messages
			WHERE conversation_id = ? AND user_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		`, checkpoint.ConversationID, userID).Scan(&currentHead)
		if errors.Is(err, sql.ErrNoRows) || currentHead != checkpoint.ExpectedHeadMessageID {
			return ContextCheckpoint{}, ErrConversationChanged
		}
		if err != nil {
			return ContextCheckpoint{}, err
		}
	}

	id, err := ids.New()
	if err != nil {
		return ContextCheckpoint{}, err
	}
	checkpoint.ID = id
	checkpoint.CreatedAt = time.Now().UnixMilli()
	if checkpoint.Status == "" {
		checkpoint.Status = "completed"
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO context_checkpoints(
			id, conversation_id, user_id, boundary_message_id, previous_checkpoint_id,
			model, summary_text, source_first_message_id, source_last_message_id,
			estimated_tokens_before, estimated_tokens_after, source_bytes,
			input_tokens, output_tokens, status, created_at
		)
		SELECT ?, c.id, c.user_id, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM conversations c
		WHERE c.id = ? AND c.user_id = ?
	`, checkpoint.ID, checkpoint.BoundaryMessageID, checkpoint.PreviousCheckpointID,
		checkpoint.Model, checkpoint.SummaryText, checkpoint.SourceFirstMessageID,
		checkpoint.SourceLastMessageID, checkpoint.EstimatedTokensBefore,
		checkpoint.EstimatedTokensAfter, checkpoint.SourceBytes, checkpoint.InputTokens,
		checkpoint.OutputTokens, checkpoint.Status, checkpoint.CreatedAt,
		checkpoint.ConversationID, userID)
	if err != nil {
		return ContextCheckpoint{}, fmt.Errorf("create context checkpoint: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ContextCheckpoint{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return ContextCheckpoint{}, err
	}
	return checkpoint, nil
}
