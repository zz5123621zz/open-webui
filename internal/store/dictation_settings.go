package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type DictationServiceSetting struct {
	Enabled   bool   `json:"enabled"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (s *Store) DictationServiceSetting(
	ctx context.Context,
) (DictationServiceSetting, error) {
	var setting DictationServiceSetting
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, COALESCE(updated_by, ''), updated_at
		FROM dictation_service_settings
		WHERE id = 1
	`).Scan(&enabled, &setting.UpdatedBy, &setting.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DictationServiceSetting{}, ErrNotFound
	}
	if err != nil {
		return DictationServiceSetting{}, fmt.Errorf(
			"read dictation service setting: %w", err,
		)
	}
	setting.Enabled = enabled == 1
	return setting, nil
}

func (s *Store) SetDictationServiceSetting(
	ctx context.Context,
	actorUserID string,
	enabled bool,
) (DictationServiceSetting, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DictationServiceSetting{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdministrator(ctx, tx, actorUserID); err != nil {
		return DictationServiceSetting{}, err
	}

	var currentEnabled int
	if err := tx.QueryRowContext(ctx, `
		SELECT enabled FROM dictation_service_settings WHERE id = 1
	`).Scan(&currentEnabled); err != nil {
		return DictationServiceSetting{}, fmt.Errorf(
			"read dictation setting for update: %w", err,
		)
	}
	now := time.Now().UnixMilli()
	nextEnabled := boolInteger(enabled)
	if _, err := tx.ExecContext(ctx, `
		UPDATE dictation_service_settings
		SET enabled = ?, updated_by = ?, updated_at = ?
		WHERE id = 1
	`, nextEnabled, actorUserID, now); err != nil {
		return DictationServiceSetting{}, fmt.Errorf(
			"update dictation service setting: %w", err,
		)
	}
	if currentEnabled != nextEnabled {
		auditID, err := ids.New()
		if err != nil {
			return DictationServiceSetting{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dictation_service_setting_audit(
				id, old_enabled, new_enabled, actor_user_id, created_at
			)
			VALUES(?, ?, ?, ?, ?)
		`, auditID, currentEnabled, nextEnabled, actorUserID, now); err != nil {
			return DictationServiceSetting{}, fmt.Errorf(
				"insert dictation setting audit: %w", err,
			)
		}
	}
	if err := tx.Commit(); err != nil {
		return DictationServiceSetting{}, err
	}
	return DictationServiceSetting{
		Enabled: enabled, UpdatedBy: actorUserID, UpdatedAt: now,
	}, nil
}
