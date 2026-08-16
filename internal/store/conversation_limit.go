package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

const MaxConfigurableActiveConversations = 10000

type ConversationLimitSetting struct {
	MaxActive int    `json:"maxActiveConversations"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

func normalizeMaxActiveConversations(maxActive int) int {
	if maxActive < 0 {
		return defaultMaxActiveConversations
	}
	return maxActive
}

func (s *Store) ConversationLimitSetting(ctx context.Context) (ConversationLimitSetting, error) {
	var setting ConversationLimitSetting
	err := s.db.QueryRowContext(ctx, `
		SELECT max_active, COALESCE(updated_by, ''), updated_at
		FROM conversation_limit_settings
		WHERE id = 1
	`).Scan(&setting.MaxActive, &setting.UpdatedBy, &setting.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationLimitSetting{}, ErrNotFound
	}
	if err != nil {
		return ConversationLimitSetting{}, fmt.Errorf("read conversation limit setting: %w", err)
	}
	return setting, nil
}

func (s *Store) SetConversationLimitSetting(
	ctx context.Context,
	actorUserID string,
	maxActive int,
) (ConversationLimitSetting, error) {
	if maxActive < 0 || maxActive > MaxConfigurableActiveConversations {
		return ConversationLimitSetting{}, fmt.Errorf(
			"max active conversations must be between 0 and %d",
			MaxConfigurableActiveConversations,
		)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConversationLimitSetting{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdministrator(ctx, tx, actorUserID); err != nil {
		return ConversationLimitSetting{}, err
	}

	var currentMax sql.NullInt64
	var currentUpdatedBy string
	var currentUpdatedAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT max_active, COALESCE(updated_by, ''), updated_at
		FROM conversation_limit_settings
		WHERE id = 1
	`).Scan(&currentMax, &currentUpdatedBy, &currentUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ConversationLimitSetting{}, fmt.Errorf("read conversation limit for update: %w", err)
	}

	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_limit_settings(id, max_active, updated_by, updated_at)
		VALUES(1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			max_active = excluded.max_active,
			updated_by = excluded.updated_by,
			updated_at = excluded.updated_at
	`, maxActive, actorUserID, now); err != nil {
		return ConversationLimitSetting{}, fmt.Errorf("update conversation limit setting: %w", err)
	}
	if err := insertConversationLimitSettingAudit(
		ctx, tx, actorUserID, currentMax, maxActive, now,
	); err != nil {
		return ConversationLimitSetting{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConversationLimitSetting{}, err
	}
	return ConversationLimitSetting{
		MaxActive: maxActive, UpdatedBy: actorUserID, UpdatedAt: now,
	}, nil
}

func insertConversationLimitSettingAudit(
	ctx context.Context,
	executor serviceSettingExecutor,
	actorUserID string,
	oldMax sql.NullInt64,
	newMax int,
	createdAt int64,
) error {
	id, err := ids.New()
	if err != nil {
		return err
	}
	var oldValue any
	if oldMax.Valid {
		oldValue = oldMax.Int64
	}
	if _, err := executor.ExecContext(ctx, `
		INSERT INTO conversation_limit_setting_audit(
			id, old_max_active, new_max_active, actor_user_id, created_at
		)
		VALUES(?, ?, ?, ?, ?)
	`, id, oldValue, newMax, actorUserID, createdAt); err != nil {
		return fmt.Errorf("insert conversation limit setting audit: %w", err)
	}
	return nil
}
