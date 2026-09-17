package media

import (
	"context"

	"github.com/google/uuid"
)

// This file defines the auth boundary the media package needs and nothing
// more: who is making the request. internal/auth is being built separately
// and media does not import it. The contract is: whatever verifies the
// caller's credentials (a JWT middleware, most likely) must call
// ContextWithUserID on the request context before it reaches the handlers
// registered by Routes. A missing value is treated as unauthenticated.

type ctxKey int

const userIDKey ctxKey = iota

// ContextWithUserID attaches the authenticated caller's id to ctx.
func ContextWithUserID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserIDFromContext returns the authenticated caller's id, or false if the
// context carries none.
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey).(uuid.UUID)
	return id, ok
}
