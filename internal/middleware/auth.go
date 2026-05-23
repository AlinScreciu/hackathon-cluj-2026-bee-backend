package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/radarul-albinelor/api/internal/domain"
	"github.com/radarul-albinelor/api/internal/platform"
)

type contextKey int

const userContextKey contextKey = iota

func RequireAuth(jwtSvc *platform.JWTService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("ra_session")
			if err != nil {
				writeAuthError(w)
				return
			}
			claims, err := jwtSvc.Verify(cookie.Value)
			if err != nil {
				writeAuthError(w)
				return
			}
			user := &domain.User{
				ID:   claims.UserID,
				Role: claims.Role,
				CNP:  claims.CNP,
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(roles ...domain.Role) func(http.Handler) http.Handler {
	allowed := make(map[domain.Role]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil || !allowed[user.Role] {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"code":    "forbidden_role",
						"message": "Acces interzis pentru rolul dumneavoastră",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserFromContext(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userContextKey).(*domain.User)
	return u
}

func WithUser(ctx context.Context, user *domain.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "unauthorized",
			"message": "Sesiune invalidă sau expirată",
		},
	})
}
