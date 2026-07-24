package httpapi

import (
	"context"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) readableConversation(
	ctx context.Context,
	session store.Session,
	conversationID string,
	includeRetained bool,
) (store.Conversation, error) {
	if session.User.Role == "admin" {
		return s.store.ConversationByIDAny(ctx, conversationID, includeRetained)
	}
	if includeRetained {
		return s.store.OwnedConversationByID(ctx, session.User.ID, conversationID)
	}
	return s.store.ConversationByID(ctx, session.User.ID, conversationID)
}
