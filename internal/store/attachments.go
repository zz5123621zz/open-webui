package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Attachment struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	ConversationID string `json:"conversationId,omitempty"`
	MessageID      string `json:"messageId,omitempty"`
	Kind           string `json:"kind"`
	OriginalName   string `json:"originalName,omitempty"`
	MediaType      string `json:"mediaType"`
	ByteSize       int64  `json:"byteSize"`
	SHA256         string `json:"sha256"`
	StoragePath    string `json:"-"`
	CreatedAt      int64  `json:"createdAt"`
}

type StorageStatus struct {
	UsedBytes              int64 `json:"usedBytes"`
	LimitBytes             int64 `json:"limitBytes"`
	RetainedBytes          int64 `json:"retainedBytes"`
	ActiveConversations    int   `json:"activeConversations"`
	MaxActiveConversations int   `json:"maxActiveConversations"`
	PinnedConversations    int   `json:"pinnedConversations"`
	MaxPinnedConversations int   `json:"maxPinnedConversations"`
	RetentionDays          int   `json:"retentionDays"`
}

func (s *Store) CreateAttachment(ctx context.Context, attachment Attachment) (Attachment, error) {
	return s.createAttachment(ctx, attachment, 0)
}

func (s *Store) CreateAttachmentWithinQuota(
	ctx context.Context,
	attachment Attachment,
	maxStorageBytes int64,
) (Attachment, error) {
	if maxStorageBytes < 1 {
		maxStorageBytes = defaultMaxStorageBytes
	}
	return s.createAttachment(ctx, attachment, maxStorageBytes)
}

func (s *Store) createAttachment(
	ctx context.Context,
	attachment Attachment,
	maxStorageBytes int64,
) (Attachment, error) {
	if attachment.CreatedAt == 0 {
		attachment.CreatedAt = time.Now().UnixMilli()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Attachment{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if maxStorageBytes > 0 {
		usedBytes, err := activeStorageBytes(ctx, tx, attachment.UserID)
		if err != nil {
			return Attachment{}, err
		}
		if attachment.ByteSize > maxStorageBytes-usedBytes {
			return Attachment{}, ErrStorageQuota
		}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO attachments(
			id, user_id, conversation_id, message_id, kind, original_name,
			media_type, byte_size, sha256, storage_path, created_at
		)
		VALUES(?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?, ?, ?)
	`, attachment.ID, attachment.UserID, attachment.ConversationID, attachment.MessageID,
		attachment.Kind, attachment.OriginalName, attachment.MediaType, attachment.ByteSize,
		attachment.SHA256, attachment.StoragePath, attachment.CreatedAt)
	if err != nil {
		return Attachment{}, fmt.Errorf("create attachment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Attachment{}, err
	}
	return attachment, nil
}

func activeStorageBytes(ctx context.Context, tx *sql.Tx, userID string) (int64, error) {
	var usedBytes int64
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(a.byte_size), 0)
		FROM attachments a
		LEFT JOIN conversations c ON c.id = a.conversation_id
		WHERE a.user_id = ? AND a.deleted_at IS NULL
		  AND (a.conversation_id IS NULL OR c.archived_at IS NULL)
	`, userID).Scan(&usedBytes)
	if err != nil {
		return 0, fmt.Errorf("calculate active storage: %w", err)
	}
	return usedBytes, nil
}

func (s *Store) StorageStatus(
	ctx context.Context,
	userID string,
	maxStorageBytes int64,
	maxActive, maxPinned int,
	retentionDays int,
) (StorageStatus, error) {
	if maxStorageBytes < 1 {
		maxStorageBytes = defaultMaxStorageBytes
	}
	status := StorageStatus{
		LimitBytes: maxStorageBytes, MaxActiveConversations: maxActive,
		MaxPinnedConversations: maxPinned, RetentionDays: retentionDays,
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE
				WHEN a.deleted_at IS NULL
				 AND (a.conversation_id IS NULL OR c.archived_at IS NULL)
				THEN a.byte_size ELSE 0 END), 0),
			COALESCE(SUM(CASE
				WHEN a.deleted_at IS NULL AND c.archived_at IS NOT NULL
				THEN a.byte_size ELSE 0 END), 0)
		FROM attachments a
		LEFT JOIN conversations c ON c.id = a.conversation_id
		WHERE a.user_id = ?
	`, userID).Scan(&status.UsedBytes, &status.RetainedBytes)
	if err != nil {
		return StorageStatus{}, fmt.Errorf("calculate storage status: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN pinned_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM conversations
		WHERE user_id = ? AND archived_at IS NULL
	`, userID).Scan(&status.ActiveConversations, &status.PinnedConversations); err != nil {
		return StorageStatus{}, fmt.Errorf("calculate conversation status: %w", err)
	}
	return status, nil
}

func (s *Store) AttachmentByID(ctx context.Context, userID, attachmentID string) (Attachment, error) {
	var attachment Attachment
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, COALESCE(conversation_id, ''), COALESCE(message_id, ''), kind,
		       COALESCE(original_name, ''), media_type, byte_size, sha256, storage_path, created_at
		FROM attachments
		WHERE id = ? AND user_id = ? AND deleted_at IS NULL
	`, attachmentID, userID).Scan(
		&attachment.ID, &attachment.UserID, &attachment.ConversationID, &attachment.MessageID,
		&attachment.Kind, &attachment.OriginalName, &attachment.MediaType, &attachment.ByteSize,
		&attachment.SHA256, &attachment.StoragePath, &attachment.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("lookup attachment: %w", err)
	}
	return attachment, nil
}

func (s *Store) AttachmentByIDAny(ctx context.Context, attachmentID string) (Attachment, error) {
	var attachment Attachment
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, COALESCE(conversation_id, ''), COALESCE(message_id, ''), kind,
		       COALESCE(original_name, ''), media_type, byte_size, sha256, storage_path, created_at
		FROM attachments
		WHERE id = ? AND deleted_at IS NULL
	`, attachmentID).Scan(
		&attachment.ID, &attachment.UserID, &attachment.ConversationID, &attachment.MessageID,
		&attachment.Kind, &attachment.OriginalName, &attachment.MediaType, &attachment.ByteSize,
		&attachment.SHA256, &attachment.StoragePath, &attachment.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("lookup attachment for administrator: %w", err)
	}
	return attachment, nil
}

func (s *Store) DeleteAttachment(ctx context.Context, userID, attachmentID string) (Attachment, error) {
	attachment, err := s.AttachmentByID(ctx, userID, attachmentID)
	if err != nil {
		return Attachment{}, err
	}
	if attachment.MessageID != "" {
		return Attachment{}, errors.New("attachment is already used by a message")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE attachments SET deleted_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL
	`, time.Now().UnixMilli(), attachmentID, userID)
	if err != nil {
		return Attachment{}, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return Attachment{}, ErrNotFound
	}
	return attachment, nil
}

func (s *Store) BindAttachments(ctx context.Context, tx *sql.Tx, userID, conversationID, messageID string, attachmentIDs []string) error {
	var totalBytes int64
	for _, attachmentID := range attachmentIDs {
		var size int64
		var mediaType string
		var existingMessageID sql.NullString
		err := tx.QueryRowContext(ctx, `
			SELECT byte_size, media_type, message_id
			FROM attachments
			WHERE id = ? AND user_id = ? AND deleted_at IS NULL
		`, attachmentID, userID).Scan(&size, &mediaType, &existingMessageID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if existingMessageID.Valid {
			return errors.New("attachment is already bound")
		}
		if mediaType != "image/png" && mediaType != "image/jpeg" && mediaType != "image/webp" {
			return errors.New("attachment is not a supported image")
		}
		totalBytes += size
		if totalBytes > 30*1024*1024 {
			return errors.New("message attachments exceed 30 MiB")
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE attachments SET conversation_id = ?, message_id = ?
			WHERE id = ? AND user_id = ? AND message_id IS NULL AND deleted_at IS NULL
		`, conversationID, messageID, attachmentID, userID)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			return errors.New("attachment binding conflict")
		}
	}
	return nil
}
