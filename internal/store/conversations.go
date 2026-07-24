package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type Conversation struct {
	ID              string `json:"id"`
	UserID          string `json:"-"`
	Title           string `json:"title"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	ArchivedAt      int64  `json:"archivedAt,omitempty"`
}

func (s *Store) CreateConversation(ctx context.Context, userID, title, model, reasoningEffort string) (Conversation, error) {
	id, err := ids.New()
	if err != nil {
		return Conversation{}, err
	}
	title = normalizeTitle(title)
	now := time.Now().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, fmt.Errorf("begin conversation creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(id, user_id, title, model, reasoning_effort, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, id, userID, title, model, reasoningEffort, now, now); err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET preferred_model = ?, updated_at = ? WHERE id = ?`, model, time.Now().Unix(), userID); err != nil {
		return Conversation{}, fmt.Errorf("save preferred model: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit conversation creation: %w", err)
	}
	return Conversation{
		ID: id, UserID: userID, Title: title, Model: model, ReasoningEffort: reasoningEffort,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) ListConversations(ctx context.Context, userID string, limit int) ([]Conversation, error) {
	return s.ListConversationsByArchive(ctx, userID, limit, false)
}

func (s *Store) ListConversationsByArchive(ctx context.Context, userID string, limit int, archived bool) ([]Conversation, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	archiveFlag := 0
	if archived {
		archiveFlag = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, title, model, reasoning_effort, created_at, updated_at,
		       COALESCE(archived_at, 0)
		FROM conversations
		WHERE user_id = ?
		  AND ((? = 0 AND archived_at IS NULL) OR (? = 1 AND archived_at IS NOT NULL))
		ORDER BY updated_at DESC, id DESC
		LIMIT ?
	`, userID, archiveFlag, archiveFlag, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	result := make([]Conversation, 0)
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(
			&conversation.ID, &conversation.UserID, &conversation.Title, &conversation.Model,
			&conversation.ReasoningEffort, &conversation.CreatedAt, &conversation.UpdatedAt,
			&conversation.ArchivedAt,
		); err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

func (s *Store) ConversationByID(ctx context.Context, userID, conversationID string) (Conversation, error) {
	return s.conversationByID(ctx, userID, conversationID, false)
}

func (s *Store) OwnedConversationByID(ctx context.Context, userID, conversationID string) (Conversation, error) {
	return s.conversationByID(ctx, userID, conversationID, true)
}

func (s *Store) conversationByID(ctx context.Context, userID, conversationID string, includeArchived bool) (Conversation, error) {
	var conversation Conversation
	query := `
		SELECT id, user_id, title, model, reasoning_effort, created_at, updated_at,
		       COALESCE(archived_at, 0)
		FROM conversations
		WHERE id = ? AND user_id = ?`
	if !includeArchived {
		query += ` AND archived_at IS NULL`
	}
	err := s.db.QueryRowContext(ctx, query, conversationID, userID).Scan(
		&conversation.ID, &conversation.UserID, &conversation.Title, &conversation.Model,
		&conversation.ReasoningEffort, &conversation.CreatedAt, &conversation.UpdatedAt,
		&conversation.ArchivedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("lookup conversation: %w", err)
	}
	return conversation, nil
}

func (s *Store) UpdateConversation(ctx context.Context, userID, conversationID, title, model, reasoningEffort string) (Conversation, error) {
	title = normalizeTitle(title)
	now := time.Now().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, fmt.Errorf("begin conversation update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET title = ?, model = ?, reasoning_effort = ?, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, title, model, reasoningEffort, now, conversationID, userID)
	if err != nil {
		return Conversation{}, fmt.Errorf("update conversation: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Conversation{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET preferred_model = ?, updated_at = ? WHERE id = ?`, model, time.Now().Unix(), userID); err != nil {
		return Conversation{}, fmt.Errorf("save preferred model: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit conversation update: %w", err)
	}
	return s.OwnedConversationByID(ctx, userID, conversationID)
}

func (s *Store) SetConversationArchived(ctx context.Context, userID, conversationID string, archived bool) (Conversation, error) {
	var value any
	if archived {
		value = time.Now().UnixMilli()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE conversations
		SET archived_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, value, time.Now().UnixMilli(), conversationID, userID)
	if err != nil {
		return Conversation{}, fmt.Errorf("set conversation archive state: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Conversation{}, ErrNotFound
	}
	return s.OwnedConversationByID(ctx, userID, conversationID)
}

func (s *Store) DeleteConversation(ctx context.Context, userID, conversationID string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM conversations WHERE id = ? AND user_id = ?
	`, conversationID, userID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT storage_path FROM attachments
		WHERE conversation_id = ? AND user_id = ?
	`, conversationID, userID)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for rows.Next() {
		var storagePath string
		if err := rows.Scan(&storagePath); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, storagePath)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM attachments WHERE conversation_id = ? AND user_id = ?
	`, conversationID, userID); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE id = ? AND user_id = ?`, conversationID, userID)
	if err != nil {
		return nil, fmt.Errorf("delete conversation: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return paths, nil
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "New chat"
	}
	runes := []rune(title)
	if len(runes) > 120 {
		title = string(runes[:120])
	}
	return title
}
