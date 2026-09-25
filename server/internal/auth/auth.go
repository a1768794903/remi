package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type contextKey string

const userIDKey contextKey = "remi.user_id"

// APIKeyVerifier authenticates a non-Firebase bearer credential. The path and
// method are provided so callers can enforce endpoint-specific scopes without
// making the core auth package depend on a particular key store.
type APIKeyVerifier func(ctx context.Context, token, method, path string) (string, error)

var ErrMissingUserID = errors.New("missing authenticated user")

// Middleware keeps the local header contract only in dev mode. Other modes
// require a verified Firebase Secure Token and never accept the dev header.
func Middleware(mode string) func(http.Handler) http.Handler {
	return MiddlewareWithVerifier(mode, nil)
}

func MiddlewareWithVerifier(mode string, verifier *FirebaseVerifier) func(http.Handler) http.Handler {
	return MiddlewareWithAPIKey(mode, verifier, nil)
}

func MiddlewareWithAPIKey(mode string, verifier *FirebaseVerifier, apiKeyVerifier APIKeyVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := ""
			var verifyErr error
			if mode == "dev" {
				userID = strings.TrimSpace(r.Header.Get("X-Remi-User-ID"))
			} else if verifier != nil || apiKeyVerifier != nil {
				token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
				if token != "" && apiKeyVerifier != nil && strings.HasPrefix(token, "omi_dev_") {
					userID, verifyErr = apiKeyVerifier(r.Context(), token, r.Method, r.URL.Path)
				} else if token != "" && verifier != nil {
					userID, verifyErr = verifier.Verify(r.Context(), token)
				}
				if verifyErr != nil {
					userID = ""
				}
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

const firebaseCertURL = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"

type FirebaseVerifier struct {
	ProjectID string
	Client    *http.Client
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
}

func NewFirebaseVerifier(projectID string) *FirebaseVerifier {
	return &FirebaseVerifier{ProjectID: strings.TrimSpace(projectID), Client: &http.Client{Timeout: 5 * time.Second}}
}

func (v *FirebaseVerifier) Verify(ctx context.Context, token string) (string, error) {
	if v == nil || v.ProjectID == "" {
		return "", errors.New("firebase project is not configured")
	}
	parsed, err := jwt.ParseWithClaims(token, jwt.MapClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, errors.New("unexpected firebase signing method")
		}
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("missing firebase key id")
		}
		keys, err := v.publicKeys(ctx)
		if err != nil {
			return nil, err
		}
		key, ok := keys[kid]
		if !ok {
			return nil, errors.New("unknown firebase key id")
		}
		return key, nil
	})
	if err != nil || !parsed.Valid {
		if err == nil {
			err = errors.New("invalid firebase token")
		}
		return "", err
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid firebase claims")
	}
	if !claims.VerifyAudience(v.ProjectID, true) || claims["iss"] != "https://securetoken.google.com/"+v.ProjectID {
		return "", errors.New("invalid firebase audience or issuer")
	}
	uid, ok := claims["sub"].(string)
	if !ok || uid == "" || len(uid) > 128 {
		return "", errors.New("invalid firebase subject")
	}
	return uid, nil
}

func (v *FirebaseVerifier) publicKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	v.mu.RLock()
	if time.Now().Before(v.expiresAt) && len(v.keys) > 0 {
		keys := v.keys
		v.mu.RUnlock()
		return keys, nil
	}
	v.mu.RUnlock()
	response, err := v.Client.Get(firebaseCertURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("firebase cert endpoint returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var certs map[string]string
	if err := json.Unmarshal(body, &certs); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey, len(certs))
	for kid, value := range certs {
		block, _ := pem.Decode([]byte(value))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		key, ok := cert.PublicKey.(*rsa.PublicKey)
		if ok {
			keys[kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("firebase cert endpoint returned no usable keys")
	}
	maxAge := time.Hour
	if cache := response.Header.Get("Cache-Control"); strings.Contains(cache, "max-age=") {
		var seconds int
		if _, err := fmt.Sscanf(cache[strings.Index(cache, "max-age=")+8:], "%d", &seconds); err == nil && seconds > 0 {
			maxAge = time.Duration(seconds) * time.Second
		}
	}
	v.mu.Lock()
	v.keys = keys
	v.expiresAt = time.Now().Add(maxAge)
	v.mu.Unlock()
	return keys, nil
}

func UserID(ctx context.Context) (string, error) {
	value, ok := ctx.Value(userIDKey).(string)
	if !ok || value == "" {
		return "", ErrMissingUserID
	}
	return value, nil
}
