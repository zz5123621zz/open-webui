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

func (s *Store) CreateAttachment(ctx context.Context, attachment Attachment) (Attachment, error) {
	if attachment.CreatedAt == 0 {
		attachment.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `
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
	return attachment, nil
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
