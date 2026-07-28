package store

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

type ConversationSearchResult struct {
	Conversation Conversation `json:"conversation"`
	Snippet      string       `json:"snippet,omitempty"`
	MatchedIn    string       `json:"matchedIn"`
}

const searchSnippetContextRunes = 36

// SearchConversations matches the query against active conversation titles and
// message text for one user. Matching is a case-insensitive substring scan;
// at this deployment's scale (a handful of users, personal chat volumes) a
// LIKE scan stays well under interactive latency without an FTS index.
func (s *Store) SearchConversations(
	ctx context.Context,
	userID string,
	query string,
	limit int,
) ([]ConversationSearchResult, error) {
	return s.searchConversations(ctx, userID, query, limit)
}

// SearchAllConversations is the administrator variant: it scans every user's
// active conversations and includes owner attribution.
func (s *Store) SearchAllConversations(
	ctx context.Context,
	query string,
	limit int,
) ([]ConversationSearchResult, error) {
	return s.searchConversations(ctx, "", query, limit)
}

func (s *Store) searchConversations(
	ctx context.Context,
	userID string,
	query string,
	limit int,
) ([]ConversationSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []ConversationSearchResult{}, nil
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	pattern := "%" + escapeLike(query) + "%"
	withOwner := userID == ""

	titleQuery := `
		SELECT ` + conversationColumns
	if withOwner {
		titleQuery += `, u.username, u.display_name`
	}
	titleQuery += `
		FROM conversations c`
	if withOwner {
		titleQuery += `
		JOIN users u ON u.id = c.user_id`
	}
	titleQuery += `
		WHERE c.archived_at IS NULL AND c.title LIKE ? ESCAPE '\'`
	titleArguments := []any{pattern}
	if !withOwner {
		titleQuery += ` AND c.user_id = ?`
		titleArguments = append(titleArguments, userID)
	}
	titleQuery += `
		ORDER BY c.updated_at DESC, c.id DESC
		LIMIT ?`
	titleArguments = append(titleArguments, limit)

	rows, err := s.db.QueryContext(ctx, titleQuery, titleArguments...)
	if err != nil {
		return nil, fmt.Errorf("search conversation titles: %w", err)
	}
	titleMatches, err := collectConversations(rows, withOwner)
	rows.Close()
	if err != nil {
		return nil, err
	}

	results := make([]ConversationSearchResult, 0, limit)
	seen := make(map[string]bool)
	for _, conversation := range titleMatches {
		results = append(results, ConversationSearchResult{
			Conversation: conversation, MatchedIn: "title",
		})
		seen[conversation.ID] = true
	}

	// GROUP BY collapses matches to one row per conversation inside SQLite, so
	// one chatty conversation cannot starve the result window and at most
	// `limit` snippet windows cross into Go. The bare text_content expression
	// pairs with MAX(m.created_at): SQLite's min/max aggregate rule makes it
	// come from that newest matching message. substr/instr count characters,
	// not bytes, and SQLite lower() folds only ASCII — the same folding LIKE
	// applies, so an instr miss (0) can only happen for exotic case pairs and
	// then degrades to a window at the start of the message.
	messageQuery := `
		SELECT ` + conversationColumns
	if withOwner {
		messageQuery += `, u.username, u.display_name`
	}
	messageQuery += `,
		       substr(
		         p.text_content,
		         max(1, instr(lower(p.text_content), lower(?)) - 120),
		         240 + length(?)
		       ),
		       MAX(m.created_at)
		FROM message_parts p
		JOIN messages m ON m.id = p.message_id
		JOIN conversations c ON c.id = m.conversation_id`
	if withOwner {
		messageQuery += `
		JOIN users u ON u.id = c.user_id`
	}
	messageQuery += `
		WHERE c.archived_at IS NULL
		  AND p.type IN (
		    'text', 'clarification', 'clarification_submission', 'task_brief'
		  )
		  AND p.text_content LIKE ? ESCAPE '\'`
	messageArguments := []any{query, query, pattern}
	if !withOwner {
		messageQuery += ` AND c.user_id = ?`
		messageArguments = append(messageArguments, userID)
	}
	messageQuery += `
		GROUP BY c.id
		ORDER BY c.updated_at DESC, c.id DESC
		LIMIT ?`
	messageArguments = append(messageArguments, limit)

	messageRows, err := s.db.QueryContext(ctx, messageQuery, messageArguments...)
	if err != nil {
		return nil, fmt.Errorf("search message text: %w", err)
	}
	defer messageRows.Close()
	for messageRows.Next() {
		if len(results) >= limit {
			break
		}
		var conversation Conversation
		var textContent string
		var newestMatch int64
		destinations := append(
			conversationDestinations(&conversation, withOwner), &textContent, &newestMatch,
		)
		if err := messageRows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan search match: %w", err)
		}
		if seen[conversation.ID] {
			continue
		}
		seen[conversation.ID] = true
		results = append(results, ConversationSearchResult{
			Conversation: conversation,
			Snippet:      searchSnippet(textContent, query),
			MatchedIn:    "message",
		})
	}
	if err := messageRows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func searchSnippet(text, query string) string {
	haystack := []rune(text)
	loweredText := strings.ToLower(text)
	loweredQuery := strings.ToLower(query)
	index := strings.Index(loweredText, loweredQuery)
	if index < 0 {
		if len(haystack) <= 2*searchSnippetContextRunes {
			return strings.Join(strings.Fields(text), " ")
		}
		return strings.Join(strings.Fields(string(haystack[:2*searchSnippetContextRunes])), " ") + "…"
	}
	// Count runes in the lowered string, where index is guaranteed to sit on a
	// rune boundary; byte offsets are not transferable to the original text
	// because lowercasing can change a rune's encoded length.
	matchStart := utf8.RuneCountInString(loweredText[:index])
	matchLength := utf8.RuneCountInString(loweredQuery)
	start := matchStart - searchSnippetContextRunes
	if start < 0 {
		start = 0
	}
	end := matchStart + matchLength + searchSnippetContextRunes
	if end > len(haystack) {
		end = len(haystack)
	}
	snippet := strings.Join(strings.Fields(string(haystack[start:end])), " ")
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(haystack) {
		snippet += "…"
	}
	return snippet
}
