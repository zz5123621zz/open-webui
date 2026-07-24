package httpapi

import (
	"context"

	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

type contextKey string

const sessionContextKey contextKey = "session"
const requestMetadataContextKey contextKey = "request-metadata"

type requestMetadata struct {
	requestID  string
	userIDHash string
}

func withSession(ctx context.Context, session store.Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, session)
}

func sessionFromContext(ctx context.Context) (store.Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(store.Session)
	return session, ok
}

func withRequestMetadata(ctx context.Context, metadata *requestMetadata) context.Context {
	return context.WithValue(ctx, requestMetadataContextKey, metadata)
}

func requestMetadataFromContext(ctx context.Context) (*requestMetadata, bool) {
	metadata, ok := ctx.Value(requestMetadataContextKey).(*requestMetadata)
	return metadata, ok
}
