package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type contextKey string

const userIDKey contextKey = "remi.user_id"

var ErrMissingUserID = errors.New("missing authenticated user")

// Middleware is intentionally dev-friendly until Firebase Admin verification
// is wired. Production mode rejects the dev header so it cannot be mistaken
// for an authorization implementation.
func Middleware(mode string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := ""
			if mode == "dev" {
				userID = strings.TrimSpace(r.Header.Get("X-Remi-User-ID"))
			}
			if userID == "" {
				if mode == "dev" {
					userID = "dev-user"
				} else {
					http.Error(w, ErrMissingUserID.Error(), http.StatusUnauthorized)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, userID)))
		})
	}
}

func UserID(ctx context.Context) (string, error) {
	value, ok := ctx.Value(userIDKey).(string)
	if !ok || value == "" {
		return "", ErrMissingUserID
	}
	return value, nil
}
