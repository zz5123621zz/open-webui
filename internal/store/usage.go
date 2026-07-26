package store

import (
	"context"
	"fmt"
	"time"
)

type UsageRow struct {
	Month            string `json:"month"`
	Model            string `json:"model"`
	OwnerID          string `json:"ownerId,omitempty"`
	OwnerUsername    string `json:"ownerUsername,omitempty"`
	OwnerDisplayName string `json:"ownerDisplayName,omitempty"`
	Responses        int64  `json:"responses"`
	InputTokens      int64  `json:"inputTokens"`
	OutputTokens     int64  `json:"outputTokens"`
	ReasoningTokens  int64  `json:"reasoningTokens"`
}

// UsageByMonth aggregates completed assistant responses for one user, grouped
// by calendar month (UTC) and model, newest month first.
func (s *Store) UsageByMonth(ctx context.Context, userID string, months int) ([]UsageRow, error) {
	return s.usageByMonth(ctx, userID, months)
}

// UsageByMonthAllUsers is the administrator variant covering every user, with
// owner attribution on each row.
func (s *Store) UsageByMonthAllUsers(ctx context.Context, months int) ([]UsageRow, error) {
	return s.usageByMonth(ctx, "", months)
}

func (s *Store) usageByMonth(ctx context.Context, userID string, months int) ([]UsageRow, error) {
	if months < 1 || months > 24 {
		months = 6
	}
	withOwner := userID == ""
	query := `
		SELECT strftime('%Y-%m', datetime(m.created_at / 1000, 'unixepoch')) AS month,
		       COALESCE(m.model, '')`
	if withOwner {
		query += `, m.user_id, u.username, u.display_name`
	}
	query += `,
		       COUNT(*),
		       SUM(COALESCE(m.input_tokens, 0)),
		       SUM(COALESCE(m.output_tokens, 0)),
		       SUM(COALESCE(m.reasoning_tokens, 0))
		FROM messages m`
	if withOwner {
		query += `
		JOIN users u ON u.id = m.user_id`
	}
	now := time.Now().UTC()
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -(months - 1), 0).UnixMilli()
	query += `
		WHERE m.role = 'assistant' AND m.completed_at IS NOT NULL AND m.created_at >= ?`
	arguments := []any{windowStart}
	if !withOwner {
		query += ` AND m.user_id = ?`
		arguments = append(arguments, userID)
	}
	query += `
		GROUP BY month, m.model`
	if withOwner {
		query += `, m.user_id`
	}
	query += `
		ORDER BY month DESC, SUM(COALESCE(m.output_tokens, 0)) DESC`
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("aggregate usage: %w", err)
	}
	defer rows.Close()

	result := make([]UsageRow, 0)
	for rows.Next() {
		var row UsageRow
		destinations := []any{&row.Month, &row.Model}
		if withOwner {
			destinations = append(destinations, &row.OwnerID, &row.OwnerUsername, &row.OwnerDisplayName)
		}
		destinations = append(
			destinations, &row.Responses, &row.InputTokens, &row.OutputTokens, &row.ReasoningTokens,
		)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan usage row: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
