package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type WorkbenchSetting struct {
	Effective  string `json:"effective"`
	Initial    string `json:"initial"`
	Preference string `json:"preference,omitempty"`
}

func validWorkbench(value string) bool {
	return value == guidance.WorkbenchGeneral || value == guidance.WorkbenchRestaurant
}

func (s *Store) WorkbenchSetting(ctx context.Context, userID string) (WorkbenchSetting, error) {
	var setting WorkbenchSetting
	err := s.db.QueryRowContext(ctx, `
		SELECT initial_workbench, COALESCE(workbench_preference, '')
		FROM users
		WHERE id = ? AND status = 'active'
	`, userID).Scan(&setting.Initial, &setting.Preference)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkbenchSetting{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchSetting{}, fmt.Errorf("read workbench setting: %w", err)
	}
	setting.Effective = setting.Initial
	if setting.Preference != "" {
		setting.Effective = setting.Preference
	}
	if !validWorkbench(setting.Effective) {
		return WorkbenchSetting{}, errors.New("stored workbench setting is invalid")
	}
	return setting, nil
}

func (s *Store) SetWorkbenchPreference(
	ctx context.Context,
	userID string,
	workbench string,
) (WorkbenchSetting, error) {
	workbench = strings.ToLower(strings.TrimSpace(workbench))
	if !validWorkbench(workbench) {
		return WorkbenchSetting{}, errors.New("invalid workbench")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET workbench_preference = ?, updated_at = ?
		WHERE id = ? AND status = 'active'
	`, workbench, time.Now().Unix(), userID)
	if err != nil {
		return WorkbenchSetting{}, fmt.Errorf("update workbench preference: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return WorkbenchSetting{}, ErrNotFound
	}
	return s.WorkbenchSetting(ctx, userID)
}

// SetInitialWorkbenchByUsername is intended for the server CLI. A non-empty
// actorUserID is recorded when a future authenticated administrator surface
// calls the same operation; CLI assignments intentionally use a NULL actor.
func (s *Store) SetInitialWorkbenchByUsername(
	ctx context.Context,
	username string,
	workbench string,
	actorUserID string,
) (WorkbenchSetting, error) {
	username = strings.TrimSpace(username)
	workbench = strings.ToLower(strings.TrimSpace(workbench))
	actorUserID = strings.TrimSpace(actorUserID)
	if username == "" || !validWorkbench(workbench) {
		return WorkbenchSetting{}, errors.New("username and a valid workbench are required")
	}
	auditID, err := ids.New()
	if err != nil {
		return WorkbenchSetting{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkbenchSetting{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var userID, oldValue string
	err = tx.QueryRowContext(ctx, `
		SELECT id, initial_workbench
		FROM users
		WHERE username = ? COLLATE NOCASE
	`, username).Scan(&userID, &oldValue)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkbenchSetting{}, ErrNotFound
	}
	if err != nil {
		return WorkbenchSetting{}, err
	}
	if actorUserID != "" {
		var role string
		if err := tx.QueryRowContext(ctx, `
			SELECT role FROM users WHERE id = ? AND status = 'active'
		`, actorUserID).Scan(&role); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return WorkbenchSetting{}, ErrNotFound
			}
			return WorkbenchSetting{}, err
		}
		if role != "admin" {
			return WorkbenchSetting{}, errors.New("workbench assignment actor must be an administrator")
		}
	}
	if oldValue != workbench {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET initial_workbench = ?, updated_at = ? WHERE id = ?
		`, workbench, time.Now().Unix(), userID); err != nil {
			return WorkbenchSetting{}, err
		}
		var actor any
		if actorUserID != "" {
			actor = actorUserID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workbench_assignment_audit(
				id, user_id, actor_user_id, old_value, new_value, created_at
			)
			VALUES(?, ?, ?, ?, ?, ?)
		`, auditID, userID, actor, oldValue, workbench, time.Now().UnixMilli()); err != nil {
			return WorkbenchSetting{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkbenchSetting{}, err
	}
	return s.WorkbenchSetting(ctx, userID)
}
