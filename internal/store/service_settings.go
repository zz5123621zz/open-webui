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
	ProgressiveSummarySettingKey = "progressive_summary_mode"
	ProgressiveSummaryModeAuto   = "auto"
	ProgressiveSummaryModeOff    = "off"
)

type ServiceSetting struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

type ServiceSettingAudit struct {
	ID          string `json:"id"`
	SettingKey  string `json:"settingKey"`
	Action      string `json:"action"`
	OldValue    string `json:"oldValue"`
	NewValue    string `json:"newValue"`
	ActorUserID string `json:"actorUserId,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

func validProgressiveSummaryMode(mode string) bool {
	return mode == ProgressiveSummaryModeAuto || mode == ProgressiveSummaryModeOff
}

func (s *Store) ProgressiveSummarySetting(ctx context.Context) (ServiceSetting, error) {
	var setting ServiceSetting
	err := s.db.QueryRowContext(ctx, `
		SELECT key, value, COALESCE(updated_by, ''), updated_at
		FROM service_settings
		WHERE key = ?
	`, ProgressiveSummarySettingKey).Scan(
		&setting.Key, &setting.Value, &setting.UpdatedBy, &setting.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ServiceSetting{}, ErrNotFound
	}
	if err != nil {
		return ServiceSetting{}, fmt.Errorf("read progressive summary setting: %w", err)
	}
	if !validProgressiveSummaryMode(setting.Value) {
		return ServiceSetting{}, errors.New("progressive summary setting contains an invalid value")
	}
	return setting, nil
}

func (s *Store) SetProgressiveSummaryMode(
	ctx context.Context,
	actorUserID string,
	mode string,
) (ServiceSetting, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !validProgressiveSummaryMode(mode) {
		return ServiceSetting{}, errors.New("progressive summary mode must be auto or off")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ServiceSetting{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdministrator(ctx, tx, actorUserID); err != nil {
		return ServiceSetting{}, err
	}

	var current ServiceSetting
	if err := tx.QueryRowContext(ctx, `
		SELECT key, value, COALESCE(updated_by, ''), updated_at
		FROM service_settings
		WHERE key = ?
	`, ProgressiveSummarySettingKey).Scan(
		&current.Key, &current.Value, &current.UpdatedBy, &current.UpdatedAt,
	); err != nil {
		return ServiceSetting{}, fmt.Errorf("read setting for update: %w", err)
	}
	now := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		UPDATE service_settings
		SET value = ?, updated_by = ?, updated_at = ?
		WHERE key = ?
	`, mode, actorUserID, now, ProgressiveSummarySettingKey); err != nil {
		return ServiceSetting{}, fmt.Errorf("update progressive summary setting: %w", err)
	}
	if err := insertServiceSettingAudit(
		ctx, tx, actorUserID, "update", current.Value, mode, now,
	); err != nil {
		return ServiceSetting{}, err
	}
	if err := tx.Commit(); err != nil {
		return ServiceSetting{}, err
	}
	return ServiceSetting{
		Key: ProgressiveSummarySettingKey, Value: mode, UpdatedBy: actorUserID, UpdatedAt: now,
	}, nil
}

func (s *Store) RecordProgressiveSummaryRecheck(
	ctx context.Context,
	actorUserID string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdministrator(ctx, tx, actorUserID); err != nil {
		return err
	}
	var value string
	if err := tx.QueryRowContext(ctx, `
		SELECT value FROM service_settings WHERE key = ?
	`, ProgressiveSummarySettingKey).Scan(&value); err != nil {
		return fmt.Errorf("read setting for recheck: %w", err)
	}
	if err := insertServiceSettingAudit(
		ctx, tx, actorUserID, "recheck", value, value, time.Now().UnixMilli(),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListServiceSettingAudit(
	ctx context.Context,
	limit int,
) ([]ServiceSettingAudit, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, setting_key, action, old_value, new_value,
		       COALESCE(actor_user_id, ''), created_at
		FROM service_setting_audit
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list service setting audit: %w", err)
	}
	defer rows.Close()
	result := make([]ServiceSettingAudit, 0)
	for rows.Next() {
		var item ServiceSettingAudit
		if err := rows.Scan(
			&item.ID, &item.SettingKey, &item.Action, &item.OldValue,
			&item.NewValue, &item.ActorUserID, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type serviceSettingExecutor interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func requireAdministrator(
	ctx context.Context,
	executor serviceSettingExecutor,
	userID string,
) error {
	var role, status string
	err := executor.QueryRowContext(ctx, `
		SELECT role, status FROM users WHERE id = ?
	`, userID).Scan(&role, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role != "admin" || status != "active" {
		return ErrNotFound
	}
	return nil
}

func insertServiceSettingAudit(
	ctx context.Context,
	executor serviceSettingExecutor,
	actorUserID string,
	action string,
	oldValue string,
	newValue string,
	createdAt int64,
) error {
	id, err := ids.New()
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, `
		INSERT INTO service_setting_audit(
			id, setting_key, action, old_value, new_value, actor_user_id, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`, id, ProgressiveSummarySettingKey, action, oldValue, newValue, actorUserID, createdAt); err != nil {
		return fmt.Errorf("insert service setting audit: %w", err)
	}
	return nil
}
