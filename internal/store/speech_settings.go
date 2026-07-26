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

const (
	SpeechModeManual = "manual"
	SpeechModeAuto   = "auto"
)

type SpeechServiceSetting struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	DefaultVoice string `json:"defaultVoice"`
	UpdatedBy    string `json:"updatedBy,omitempty"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type UserSpeechPreference struct {
	UserID    string  `json:"-"`
	Mode      string  `json:"mode"`
	Speed     float64 `json:"speed"`
	Voice     string  `json:"voice"`
	UpdatedAt int64   `json:"updatedAt"`
}

func (s *Store) SpeechServiceSetting(ctx context.Context) (SpeechServiceSetting, error) {
	var setting SpeechServiceSetting
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT enabled, provider, default_voice, COALESCE(updated_by, ''), updated_at
		FROM speech_service_settings
		WHERE id = 1
	`).Scan(
		&enabled, &setting.Provider, &setting.DefaultVoice,
		&setting.UpdatedBy, &setting.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SpeechServiceSetting{}, ErrNotFound
	}
	if err != nil {
		return SpeechServiceSetting{}, fmt.Errorf("read speech service setting: %w", err)
	}
	setting.Enabled = enabled == 1
	return setting, nil
}

func (s *Store) SetSpeechServiceSetting(
	ctx context.Context,
	actorUserID string,
	next SpeechServiceSetting,
) (SpeechServiceSetting, error) {
	next.Provider = strings.ToLower(strings.TrimSpace(next.Provider))
	next.DefaultVoice = strings.TrimSpace(next.DefaultVoice)
	if next.Provider == "" || len(next.Provider) > 64 {
		return SpeechServiceSetting{}, errors.New("speech provider is invalid")
	}
	if next.DefaultVoice == "" || len(next.DefaultVoice) > 100 {
		return SpeechServiceSetting{}, errors.New("default voice is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SpeechServiceSetting{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdministrator(ctx, tx, actorUserID); err != nil {
		return SpeechServiceSetting{}, err
	}
	var current SpeechServiceSetting
	var enabled int
	if err := tx.QueryRowContext(ctx, `
		SELECT enabled, provider, default_voice, COALESCE(updated_by, ''), updated_at
		FROM speech_service_settings WHERE id = 1
	`).Scan(
		&enabled, &current.Provider, &current.DefaultVoice,
		&current.UpdatedBy, &current.UpdatedAt,
	); err != nil {
		return SpeechServiceSetting{}, fmt.Errorf("read speech setting for update: %w", err)
	}
	current.Enabled = enabled == 1
	now := time.Now().UnixMilli()
	next.UpdatedBy = actorUserID
	next.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		UPDATE speech_service_settings
		SET enabled = ?, provider = ?, default_voice = ?, updated_by = ?, updated_at = ?
		WHERE id = 1
	`, boolInteger(next.Enabled), next.Provider, next.DefaultVoice, actorUserID, now); err != nil {
		return SpeechServiceSetting{}, fmt.Errorf("update speech service setting: %w", err)
	}
	if err := insertSpeechServiceSettingAudit(ctx, tx, actorUserID, current, next, now); err != nil {
		return SpeechServiceSetting{}, err
	}
	if err := tx.Commit(); err != nil {
		return SpeechServiceSetting{}, err
	}
	return next, nil
}

func (s *Store) UserSpeechPreference(
	ctx context.Context,
	userID string,
) (UserSpeechPreference, error) {
	var preference UserSpeechPreference
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id,
		       COALESCE(p.mode, 'manual'),
		       COALESCE(p.speed, 1.0),
		       COALESCE(p.voice, ''),
		       COALESCE(p.updated_at, 0)
		FROM users u
		LEFT JOIN user_speech_preferences p ON p.user_id = u.id
		WHERE u.id = ? AND u.status = 'active'
	`, userID).Scan(
		&preference.UserID, &preference.Mode, &preference.Speed,
		&preference.Voice, &preference.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return UserSpeechPreference{}, ErrNotFound
	}
	if err != nil {
		return UserSpeechPreference{}, fmt.Errorf("read user speech preference: %w", err)
	}
	return preference, nil
}

func (s *Store) SetUserSpeechPreference(
	ctx context.Context,
	userID string,
	preference UserSpeechPreference,
) (UserSpeechPreference, error) {
	preference.Mode = strings.ToLower(strings.TrimSpace(preference.Mode))
	preference.Voice = strings.TrimSpace(preference.Voice)
	if preference.Mode != SpeechModeManual && preference.Mode != SpeechModeAuto {
		return UserSpeechPreference{}, errors.New("speech mode must be manual or auto")
	}
	if preference.Speed < 0.5 || preference.Speed > 2 {
		return UserSpeechPreference{}, errors.New("speech speed must be between 0.5 and 2.0")
	}
	if len(preference.Voice) > 100 {
		return UserSpeechPreference{}, errors.New("speech voice is invalid")
	}
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO user_speech_preferences(user_id, mode, speed, voice, updated_at)
		SELECT id, ?, ?, ?, ?
		FROM users
		WHERE id = ? AND status = 'active'
		ON CONFLICT(user_id) DO UPDATE SET
			mode = excluded.mode,
			speed = excluded.speed,
			voice = excluded.voice,
			updated_at = excluded.updated_at
	`, preference.Mode, preference.Speed, preference.Voice, now, userID)
	if err != nil {
		return UserSpeechPreference{}, fmt.Errorf("update user speech preference: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return UserSpeechPreference{}, ErrNotFound
	}
	preference.UserID = userID
	preference.UpdatedAt = now
	return preference, nil
}

func insertSpeechServiceSettingAudit(
	ctx context.Context,
	executor serviceSettingExecutor,
	actorUserID string,
	oldValue SpeechServiceSetting,
	newValue SpeechServiceSetting,
	createdAt int64,
) error {
	id, err := ids.New()
	if err != nil {
		return err
	}
	oldJSON, err := json.Marshal(oldValue)
	if err != nil {
		return err
	}
	newJSON, err := json.Marshal(newValue)
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, `
		INSERT INTO speech_service_setting_audit(
			id, old_value_json, new_value_json, actor_user_id, created_at
		)
		VALUES(?, ?, ?, ?, ?)
	`, id, string(oldJSON), string(newJSON), actorUserID, createdAt); err != nil {
		return fmt.Errorf("insert speech service setting audit: %w", err)
	}
	return nil
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}
