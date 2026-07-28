package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/ids"
)

type RestaurantProfileFact struct {
	Field     string `json:"field"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updatedAt"`
}

func (s *Store) RestaurantProfile(
	ctx context.Context,
	userID string,
) ([]RestaurantProfileFact, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND status = 'active'
	`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT field_key, value, updated_at
		FROM restaurant_profile_facts
		WHERE user_id = ?
		ORDER BY field_key
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("read restaurant profile: %w", err)
	}
	defer rows.Close()
	facts := make([]RestaurantProfileFact, 0)
	for rows.Next() {
		var fact RestaurantProfileFact
		if err := rows.Scan(&fact.Field, &fact.Value, &fact.UpdatedAt); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func applyRestaurantProfileMutation(
	ctx context.Context,
	tx *sql.Tx,
	userID string,
	sourceMessageID string,
	mutation guidance.ProfileMutation,
) error {
	if !slices.Contains(guidance.RestaurantProfileFields, mutation.Field) {
		return errors.New("restaurant profile field is invalid")
	}
	mutation.Value = strings.TrimSpace(mutation.Value)
	var oldValue string
	err := tx.QueryRowContext(ctx, `
		SELECT value
		FROM restaurant_profile_facts
		WHERE user_id = ? AND field_key = ?
	`, userID, mutation.Field).Scan(&oldValue)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UnixMilli()
	switch mutation.Operation {
	case "set":
		if exists {
			return ErrStaleGuidance
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO restaurant_profile_facts(
				user_id, field_key, value, source_message_id, created_at, updated_at
			)
			VALUES(?, ?, ?, ?, ?, ?)
		`, userID, mutation.Field, mutation.Value, sourceMessageID, now, now); err != nil {
			return err
		}
	case "replace":
		if !exists {
			return ErrStaleGuidance
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE restaurant_profile_facts
			SET value = ?, source_message_id = ?, updated_at = ?
			WHERE user_id = ? AND field_key = ?
		`, mutation.Value, sourceMessageID, now, userID, mutation.Field); err != nil {
			return err
		}
	case "delete":
		if !exists {
			return ErrStaleGuidance
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM restaurant_profile_facts
			WHERE user_id = ? AND field_key = ?
		`, userID, mutation.Field); err != nil {
			return err
		}
	default:
		return errors.New("restaurant profile operation is invalid")
	}
	auditID, err := ids.New()
	if err != nil {
		return err
	}
	var oldAuditValue, newAuditValue any
	if exists {
		oldAuditValue = oldValue
	}
	if mutation.Operation != "delete" {
		newAuditValue = mutation.Value
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO restaurant_profile_audit(
			id, user_id, field_key, operation, old_value, new_value,
			source_message_id, created_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, auditID, userID, mutation.Field, mutation.Operation, oldAuditValue,
		newAuditValue, sourceMessageID, now); err != nil {
		return err
	}
	return nil
}
