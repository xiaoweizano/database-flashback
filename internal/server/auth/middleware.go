package auth

import (
	"context"
	"net/http"
	"strings"
)

// contextKey is an unexported type used for context keys to avoid collisions.
type contextKey string

const claimsKey contextKey = "auth_claims"

// ClaimsFromContext extracts the JWT claims previously stored in the request
// context by AuthMiddleware. Returns nil if no claims are present.
func ClaimsFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey).(*Claims)
	return claims
}

// AuthMiddleware is an HTTP middleware that validates a Bearer JWT token from
// the Authorization header and injects the parsed claims into the request
// context. Requests without a valid token receive a 401 response.
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, http.StatusUnauthorized, "invalid Authorization header format")
			return
		}

		tokenStr := parts[1]
		claims, err := ValidateToken(tokenStr, h.jwtSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}

		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
