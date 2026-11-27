package middleware

import (
	"context"
	"net/http"
	"strings"

	"privasync/server/internal/auth"
	"privasync/server/internal/model"
)

type contextKey string

const (
	ContextKeyClaims contextKey = "claims"
)

// SSECookieName carries the event-stream credential. The browser's EventSource
// cannot set request headers, so this is how a web client authenticates the
// stream; the desktop client uses a normal Bearer header instead.
const SSECookieName = "sse_token"

// Authenticate validates the Bearer access token and stores the claims in the
// context.
func Authenticate(jwtSecret string) func(http.Handler) http.Handler {
	return authenticateWith(func(r *http.Request) *auth.Claims {
		return parseTyped(jwtSecret, extractBearer(r), auth.TokenTypeAccess)
	})
}

// AuthenticateSSE guards the event stream, which has two kinds of caller: the
// desktop client sends a normal Bearer access token, while a browser's
// EventSource cannot set headers at all and presents the SSE cookie instead.
//
// The cookie deliberately carries its own token type: it is sent
// automatically on every request to its path, so it must not be usable as a
// general API credential if it ever escapes.
func AuthenticateSSE(jwtSecret string) func(http.Handler) http.Handler {
	return authenticateWith(func(r *http.Request) *auth.Claims {
		if claims := parseTyped(jwtSecret, extractBearer(r), auth.TokenTypeAccess); claims != nil {
			return claims
		}
		c, err := r.Cookie(SSECookieName)
		if err != nil {
			return nil
		}
		return parseTyped(jwtSecret, c.Value, auth.TokenTypeSSE)
	})
}

// authenticateWith turns a claims resolver into middleware: no claims means
// 401, otherwise they go into the context for the handler.
func authenticateWith(resolve func(*http.Request) *auth.Claims) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := resolve(r)
			if claims == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), ContextKeyClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// parseTyped returns the claims only when the token is valid and carries
// exactly the expected type; nil otherwise.
func parseTyped(jwtSecret, token string, want auth.TokenType) *auth.Claims {
	if token == "" {
		return nil
	}
	claims, err := auth.ParseToken(jwtSecret, token)
	if err != nil || claims.TokenType != want {
		return nil
	}
	return claims
}

// RequireAdmin returns 403 when the user lacks the admin role.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil || claims.Role != model.RoleAdmin {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ClaimsFromContext(ctx context.Context) *auth.Claims {
	v := ctx.Value(ContextKeyClaims)
	if v == nil {
		return nil
	}
	c, _ := v.(*auth.Claims)
	return c
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return h[7:]
	}
	return ""
}
