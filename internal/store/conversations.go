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

const (
	defaultMaxActiveConversations = 30
	defaultMaxStorageBytes        = int64(3 * 1024 * 1024 * 1024)
)

type Conversation struct {
	ID               string `json:"id"`
	UserID           string `json:"ownerId,omitempty"`
	OwnerUsername    string `json:"ownerUsername,omitempty"`
	OwnerDisplayName string `json:"ownerDisplayName,omitempty"`
	Title            string `json:"title"`
	Model            string `json:"model"`
	ReasoningEffort  string `json:"reasoningEffort"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
	ArchivedAt       int64  `json:"archivedAt,omitempty"`
	PinnedAt         int64  `json:"pinnedAt,omitempty"`
	RetentionReason  string `json:"retentionReason,omitempty"`
}

type conversationScanner interface {
	Scan(dest ...any) error
}

// conversationDestinations is the single source of truth for the scan targets
// matching conversationColumns (plus the owner columns when selected); every
// query that selects those columns must build its destination list here.
func conversationDestinations(conversation *Conversation, withOwner bool) []any {
	destinations := []any{
		&conversation.ID, &conversation.UserID, &conversation.Title, &conversation.Model,
		&conversation.ReasoningEffort, &conversation.CreatedAt, &conversation.UpdatedAt,
		&conversation.ArchivedAt, &conversation.PinnedAt, &conversation.RetentionReason,
	}
	if withOwner {
		destinations = append(destinations, &conversation.OwnerUsername, &conversation.OwnerDisplayName)
	}
	return destinations
}

func scanConversation(scanner conversationScanner, withOwner bool) (Conversation, error) {
	var conversation Conversation
	if err := scanner.Scan(conversationDestinations(&conversation, withOwner)...); err != nil {
		return Conversation{}, err
	}
	return conversation, nil
}

const conversationColumns = `
	c.id, c.user_id, c.title, c.model, c.reasoning_effort, c.created_at, c.updated_at,
	COALESCE(c.archived_at, 0), COALESCE(c.pinned_at, 0),
	COALESCE(c.retention_reason, '')`

func (s *Store) CreateConversation(
	ctx context.Context,
	userID, title, model, reasoningEffort string,
) (Conversation, error) {
	return s.CreateConversationWithLimit(
		ctx, userID, title, model, reasoningEffort, defaultMaxActiveConversations,
	)
}

func (s *Store) CreateConversationWithLimit(
	ctx context.Context,
	userID, title, model, reasoningEffort string,
	maxActive int,
) (Conversation, error) {
	if maxActive < 1 {
		maxActive = defaultMaxActiveConversations
	}
	title = normalizeTitle(title)
	now := time.Now().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, fmt.Errorf("begin conversation creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if title == "New chat" {
		existing, lookupErr := scanConversation(tx.QueryRowContext(ctx, `
			SELECT `+conversationColumns+`
			FROM conversations c
			WHERE c.user_id = ? AND c.archived_at IS NULL AND c.title = 'New chat'
			  AND NOT EXISTS (
				SELECT 1 FROM messages m WHERE m.conversation_id = c.id
			  )
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT 1
		`, userID), false)
		switch {
		case lookupErr == nil:
			if _, err := tx.ExecContext(ctx, `
				UPDATE conversations
				SET model = ?, reasoning_effort = ?, updated_at = ?
				WHERE id = ? AND user_id = ?
			`, model, reasoningEffort, now, existing.ID, userID); err != nil {
				return Conversation{}, fmt.Errorf("refresh empty conversation: %w", err)
			}
			if err := savePreferredModel(ctx, tx, userID, model); err != nil {
				return Conversation{}, err
			}
			if err := tx.Commit(); err != nil {
				return Conversation{}, fmt.Errorf("commit empty conversation reuse: %w", err)
			}
			existing.Model = model
			existing.ReasoningEffort = reasoningEffort
			existing.UpdatedAt = now
			return existing, nil
		case !errors.Is(lookupErr, sql.ErrNoRows):
			return Conversation{}, fmt.Errorf("find empty conversation: %w", lookupErr)
		}
	}

	var activeCount int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM conversations
		WHERE user_id = ? AND archived_at IS NULL
	`, userID).Scan(&activeCount); err != nil {
		return Conversation{}, fmt.Errorf("count active conversations: %w", err)
	}
	toRetain := activeCount - maxActive + 1
	if toRetain > 0 {
		rows, err := tx.QueryContext(ctx, `
			SELECT id
			FROM conversations
			WHERE user_id = ? AND archived_at IS NULL AND pinned_at IS NULL
			ORDER BY updated_at ASC, id ASC
			LIMIT ?
		`, userID, toRetain)
		if err != nil {
			return Conversation{}, fmt.Errorf("select conversations for retention: %w", err)
		}
		candidates := make([]string, 0, toRetain)
		for rows.Next() {
			var conversationID string
			if err := rows.Scan(&conversationID); err != nil {
				rows.Close()
				return Conversation{}, err
			}
			candidates = append(candidates, conversationID)
		}
		if err := rows.Close(); err != nil {
			return Conversation{}, err
		}
		if len(candidates) < toRetain {
			return Conversation{}, ErrConversationLimit
		}
		for _, conversationID := range candidates {
			if _, err := tx.ExecContext(ctx, `
				UPDATE conversations
				SET archived_at = ?, retention_reason = 'conversation_limit',
				    pinned_at = NULL, updated_at = ?
				WHERE id = ? AND user_id = ?
			`, now, now, conversationID, userID); err != nil {
				return Conversation{}, fmt.Errorf("retain old conversation: %w", err)
			}
		}
	}

	id, err := ids.New()
	if err != nil {
		return Conversation{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversations(
			id, user_id, title, model, reasoning_effort, created_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, id, userID, title, model, reasoningEffort, now, now); err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	if err := savePreferredModel(ctx, tx, userID, model); err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit conversation creation: %w", err)
	}
	return Conversation{
		ID: id, UserID: userID, Title: title, Model: model, ReasoningEffort: reasoningEffort,
		CreatedAt: now, UpdatedAt: now,
	}, nil
}

func savePreferredModel(ctx context.Context, tx *sql.Tx, userID, model string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET preferred_model = ?, updated_at = ? WHERE id = ?
	`, model, time.Now().Unix(), userID); err != nil {
		return fmt.Errorf("save preferred model: %w", err)
	}
	return nil
}

func (s *Store) ListConversations(ctx context.Context, userID string, limit int) ([]Conversation, error) {
	return s.ListConversationsByArchive(ctx, userID, limit, false)
}

func (s *Store) ListConversationsByArchive(
	ctx context.Context,
	userID string,
	limit int,
	archived bool,
) ([]Conversation, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	archiveFlag := 0
	if archived {
		archiveFlag = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conversationColumns+`
		FROM conversations c
		WHERE c.user_id = ?
		  AND ((? = 0 AND c.archived_at IS NULL) OR (? = 1 AND c.archived_at IS NOT NULL))
		ORDER BY
		  CASE WHEN c.pinned_at IS NULL THEN 1 ELSE 0 END,
		  c.pinned_at DESC,
		  c.updated_at DESC,
		  c.id DESC
		LIMIT ?
	`, userID, archiveFlag, archiveFlag, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	return collectConversations(rows, false)
}

func (s *Store) ListAllConversationsByArchive(
	ctx context.Context,
	limit int,
	archived bool,
) ([]Conversation, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	archiveFlag := 0
	if archived {
		archiveFlag = 1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+conversationColumns+`, u.username, u.display_name
		FROM conversations c
		JOIN users u ON u.id = c.user_id
		WHERE ((? = 0 AND c.archived_at IS NULL) OR (? = 1 AND c.archived_at IS NOT NULL))
		ORDER BY
		  CASE WHEN c.pinned_at IS NULL THEN 1 ELSE 0 END,
		  c.pinned_at DESC,
		  c.updated_at DESC,
		  c.id DESC
		LIMIT ?
	`, archiveFlag, archiveFlag, limit)
	if err != nil {
		return nil, fmt.Errorf("list all conversations: %w", err)
	}
	defer rows.Close()
	return collectConversations(rows, true)
}

func collectConversations(rows *sql.Rows, withOwner bool) ([]Conversation, error) {
	result := make([]Conversation, 0)
	for rows.Next() {
		conversation, err := scanConversation(rows, withOwner)
		if err != nil {
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

func (s *Store) conversationByID(
	ctx context.Context,
	userID, conversationID string,
	includeArchived bool,
) (Conversation, error) {
	query := `
		SELECT ` + conversationColumns + `
		FROM conversations c
		WHERE c.id = ? AND c.user_id = ?`
	if !includeArchived {
		query += ` AND c.archived_at IS NULL`
	}
	conversation, err := scanConversation(
		s.db.QueryRowContext(ctx, query, conversationID, userID),
		false,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("lookup conversation: %w", err)
	}
	return conversation, nil
}

func (s *Store) ConversationByIDAny(
	ctx context.Context,
	conversationID string,
	includeArchived bool,
) (Conversation, error) {
	query := `
		SELECT ` + conversationColumns + `, u.username, u.display_name
		FROM conversations c
		JOIN users u ON u.id = c.user_id
		WHERE c.id = ?`
	if !includeArchived {
		query += ` AND c.archived_at IS NULL`
	}
	conversation, err := scanConversation(s.db.QueryRowContext(ctx, query, conversationID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("lookup conversation for administrator: %w", err)
	}
	return conversation, nil
}

func (s *Store) UpdateConversation(
	ctx context.Context,
	userID, conversationID, title, model, reasoningEffort string,
) (Conversation, error) {
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
	if err := savePreferredModel(ctx, tx, userID, model); err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("commit conversation update: %w", err)
	}
	return s.OwnedConversationByID(ctx, userID, conversationID)
}

func (s *Store) SetConversationPinned(
	ctx context.Context,
	userID, conversationID string,
	pinned bool,
	maxPinned int,
) (Conversation, error) {
	if maxPinned < 0 {
		maxPinned = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentPinned sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT pinned_at
		FROM conversations
		WHERE id = ? AND user_id = ? AND archived_at IS NULL
	`, conversationID, userID).Scan(&currentPinned); errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	} else if err != nil {
		return Conversation{}, err
	}
	if pinned && !currentPinned.Valid {
		var pinnedCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM conversations
			WHERE user_id = ? AND archived_at IS NULL AND pinned_at IS NOT NULL
		`, userID).Scan(&pinnedCount); err != nil {
			return Conversation{}, err
		}
		if pinnedCount >= maxPinned {
			return Conversation{}, ErrPinLimit
		}
	}

	var pinnedAt any
	if pinned {
		pinnedAt = time.Now().UnixMilli()
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE conversations
		SET pinned_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ? AND archived_at IS NULL
	`, pinnedAt, time.Now().UnixMilli(), conversationID, userID)
	if err != nil {
		return Conversation{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Conversation{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, err
	}
	return s.OwnedConversationByID(ctx, userID, conversationID)
}

func (s *Store) SetConversationArchived(
	ctx context.Context,
	userID, conversationID string,
	archived bool,
) (Conversation, error) {
	return s.SetConversationArchivedWithPolicy(
		ctx,
		userID,
		conversationID,
		archived,
		defaultMaxActiveConversations,
		defaultMaxStorageBytes,
	)
}

func (s *Store) SetConversationArchivedWithPolicy(
	ctx context.Context,
	userID, conversationID string,
	archived bool,
	maxActive int,
	maxStorageBytes int64,
) (Conversation, error) {
	if maxActive < 1 {
		maxActive = defaultMaxActiveConversations
	}
	if maxStorageBytes < 1 {
		maxStorageBytes = defaultMaxStorageBytes
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var archivedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT archived_at FROM conversations WHERE id = ? AND user_id = ?
	`, conversationID, userID).Scan(&archivedAt); errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	} else if err != nil {
		return Conversation{}, err
	}

	now := time.Now().UnixMilli()
	if archived {
		if _, err := tx.ExecContext(ctx, `
			UPDATE conversations
			SET archived_at = ?, retention_reason = 'manual', pinned_at = NULL, updated_at = ?
			WHERE id = ? AND user_id = ?
		`, now, now, conversationID, userID); err != nil {
			return Conversation{}, fmt.Errorf("retain conversation: %w", err)
		}
	} else {
		if !archivedAt.Valid {
			if err := tx.Commit(); err != nil {
				return Conversation{}, err
			}
			return s.OwnedConversationByID(ctx, userID, conversationID)
		}
		var activeCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM conversations
			WHERE user_id = ? AND archived_at IS NULL
		`, userID).Scan(&activeCount); err != nil {
			return Conversation{}, err
		}
		if activeCount >= maxActive {
			return Conversation{}, ErrConversationLimit
		}
		usedBytes, err := activeStorageBytes(ctx, tx, userID)
		if err != nil {
			return Conversation{}, err
		}
		var restoringBytes int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(byte_size), 0)
			FROM attachments
			WHERE user_id = ? AND conversation_id = ? AND deleted_at IS NULL
		`, userID, conversationID).Scan(&restoringBytes); err != nil {
			return Conversation{}, err
		}
		if usedBytes+restoringBytes > maxStorageBytes {
			return Conversation{}, ErrStorageQuota
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE conversations
			SET archived_at = NULL, retention_reason = NULL, updated_at = ?
			WHERE id = ? AND user_id = ?
		`, now, conversationID, userID); err != nil {
			return Conversation{}, fmt.Errorf("restore conversation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, err
	}
	return s.OwnedConversationByID(ctx, userID, conversationID)
}

func (s *Store) PurgeExpiredRetained(
	ctx context.Context,
	archivedBefore int64,
) ([]string, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT a.storage_path
		FROM attachments a
		JOIN conversations c ON c.id = a.conversation_id
		WHERE c.archived_at IS NOT NULL AND c.archived_at <= ?
	`, archivedBefore)
	if err != nil {
		return nil, 0, err
	}
	paths := make([]string, 0)
	for rows.Next() {
		var storagePath string
		if err := rows.Scan(&storagePath); err != nil {
			rows.Close()
			return nil, 0, err
		}
		paths = append(paths, storagePath)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM attachments
		WHERE conversation_id IN (
			SELECT id FROM conversations
			WHERE archived_at IS NOT NULL AND archived_at <= ?
		)
	`, archivedBefore); err != nil {
		return nil, 0, fmt.Errorf("delete retained attachments: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM conversations
		WHERE archived_at IS NOT NULL AND archived_at <= ?
	`, archivedBefore)
	if err != nil {
		return nil, 0, fmt.Errorf("purge retained conversations: %w", err)
	}
	deleted, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return paths, deleted, nil
}

func (s *Store) DeleteConversation(
	ctx context.Context,
	userID, conversationID string,
) ([]string, error) {
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
	result, err := tx.ExecContext(ctx, `
		DELETE FROM conversations WHERE id = ? AND user_id = ?
	`, conversationID, userID)
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
