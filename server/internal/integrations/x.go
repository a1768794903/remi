package integrations

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"remi/server/internal/chat"
	"strconv"
	"strings"
	"time"
)

const defaultXDeepLink = "omi://x/callback"

func validXPostKind(kind string) bool { return kind == "tweet" || kind == "bookmark" || kind == "like" }

func xPKCE() (string, string, error) {
	b := make([]byte, 64)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier := strings.TrimRight(base64.RawURLEncoding.EncodeToString(b), "=")
	digest := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func xOAuthConfigured() bool {
	return os.Getenv("X_OAUTH_CLIENT_ID") != "" && os.Getenv("X_OAUTH_CLIENT_SECRET") != "" && os.Getenv("X_OAUTH_REDIRECT_URI") != ""
}

func xOAuthURL(state, challenge string) string {
	query := url.Values{"response_type": {"code"}, "client_id": {os.Getenv("X_OAUTH_CLIENT_ID")}, "redirect_uri": {os.Getenv("X_OAUTH_REDIRECT_URI")}, "scope": {xScope()}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return "https://x.com/i/oauth2/authorize?" + query.Encode()
}
func xScope() string {
	if value := os.Getenv("X_OAUTH_SCOPES"); value != "" {
		return value
	}
	return "tweet.read users.read bookmark.read like.read offline.access"
}

func (h Handler) XOAuthURL(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if !xOAuthConfigured() {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "x_oauth_not_configured"})
		return
	}
	verifier, challenge, err := xPKCE()
	if err != nil {
		http.Error(w, "internal_error", 500)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "internal_error", 500)
		return
	}
	deep := r.URL.Query().Get("success_redirect_url")
	if deep == "" {
		deep = defaultXDeepLink
	}
	if h.Service.Redis == nil {
		http.Error(w, "X OAuth state storage is not configured", 503)
		return
	}
	payload, _ := json.Marshal(map[string]string{"uid": u, "verifier": verifier, "success_redirect_url": deep})
	if err := h.Service.Redis.Set(r.Context(), "x_oauth_state:"+state, payload, 10*time.Minute).Err(); err != nil {
		http.Error(w, "could not store OAuth state", 503)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "auth_url": xOAuthURL(state, challenge)})
}

func (h Handler) XOAuthCallback(w http.ResponseWriter, r *http.Request) {
	deep := defaultXDeepLink
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" || r.URL.Query().Get("state") == "" {
		h.xRedirect(w, deep+"?error=missing_code", false, "Connection cancelled")
		return
	}
	if h.Service.Redis == nil {
		h.xRedirect(w, deep+"?error=config_error", false, "Connection failed")
		return
	}
	raw, err := h.Service.Redis.GetDel(r.Context(), "x_oauth_state:"+r.URL.Query().Get("state")).Result()
	if err != nil {
		h.xRedirect(w, deep+"?error=invalid_state", false, "Link expired")
		return
	}
	var state struct {
		UID      string `json:"uid"`
		Verifier string `json:"verifier"`
		Redirect string `json:"success_redirect_url"`
	}
	if json.Unmarshal([]byte(raw), &state) != nil || state.UID == "" {
		h.xRedirect(w, deep+"?error=invalid_state", false, "Link expired")
		return
	}
	if state.Redirect != "" {
		deep = state.Redirect
	}
	token, err := h.xExchange(r.Context(), r.URL.Query().Get("code"), state.Verifier)
	if err != nil {
		h.xRedirect(w, deep+"?error=exchange_failed", false, "Connection failed")
		return
	}
	integration := map[string]any{"connected": true, "access_token": token.AccessToken, "scope": token.Scope, "expires_at": time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)}
	if token.RefreshToken != "" {
		integration["refresh_token"] = token.RefreshToken
	}
	if err := h.Service.Save(r.Context(), state.UID, "x", integration); err != nil {
		h.xRedirect(w, deep+"?error=storage_failed", false, "Connection failed")
		return
	}
	h.xRedirect(w, deep+"?status=success", true, "X connected")
}

type xToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func xExpiry(value any) time.Time {
	raw, _ := value.(string)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	if parsed.Location() == time.Local {
		return parsed.UTC()
	}
	return parsed
}

func (h Handler) xExchange(ctx context.Context, code, verifier string) (xToken, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {os.Getenv("X_OAUTH_REDIRECT_URI")}, "code_verifier": {verifier}, "client_id": {os.Getenv("X_OAUTH_CLIENT_ID")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.x.com/2/oauth2/token", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return xToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(os.Getenv("X_OAUTH_CLIENT_ID"), os.Getenv("X_OAUTH_CLIENT_SECRET"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return xToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return xToken{}, fmt.Errorf("x token exchange returned %s", resp.Status)
	}
	var token xToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil || token.AccessToken == "" {
		return xToken{}, errors.New("X token response missing access token")
	}
	return token, nil
}

func (h Handler) xRefresh(ctx context.Context, refreshToken string) (xToken, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {os.Getenv("X_OAUTH_CLIENT_ID")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.x.com/2/oauth2/token", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return xToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(os.Getenv("X_OAUTH_CLIENT_ID"), os.Getenv("X_OAUTH_CLIENT_SECRET"))
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return xToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return xToken{}, fmt.Errorf("x token refresh returned %s", resp.Status)
	}
	var token xToken
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil || token.AccessToken == "" {
		return xToken{}, errors.New("X refresh response missing access token")
	}
	return token, nil
}

func (h Handler) validXToken(ctx context.Context, uid string, integration map[string]any) (string, error) {
	token, _ := integration["access_token"].(string)
	if token == "" {
		return "", errors.New("X access token is missing")
	}
	expires := xExpiry(integration["expires_at"])
	if expires.IsZero() || time.Now().UTC().Before(expires.Add(-60*time.Second)) {
		return token, nil
	}
	refresh, _ := integration["refresh_token"].(string)
	if refresh == "" {
		return token, nil
	}
	refreshed, err := h.xRefresh(ctx, refresh)
	if err != nil {
		return "", err
	}
	values := map[string]any{"access_token": refreshed.AccessToken, "expires_at": time.Now().UTC().Add(time.Duration(refreshed.ExpiresIn) * time.Second).Format(time.RFC3339)}
	if refreshed.RefreshToken != "" {
		values["refresh_token"] = refreshed.RefreshToken
	}
	if refreshed.Scope != "" {
		values["scope"] = refreshed.Scope
	}
	if err := h.Service.Save(ctx, uid, "x", values); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (h Handler) xRedirect(w http.ResponseWriter, target string, success bool, message string) {
	icon := "⚠️"
	if success {
		icon = "✓"
	}
	safe := html.EscapeString(target)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, fmt.Sprintf("<!doctype html><html><body><div>%s</div><p>%s</p><meta http-equiv=\"refresh\" content=\"0;url=%s\"><script>location.href=%q</script></body></html>", icon, html.EscapeString(message), safe, target))
}

func (h Handler) XConnectionStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	integration, err := h.Service.Raw(r.Context(), u, "x")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	connected, _ := integration["connected"].(bool)
	if !connected {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "connected": false, "post_count": 0, "memory_count": 0, "syncing": false})
		return
	}
	postCount := 0
	if h.DB != nil {
		_ = h.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM x_posts WHERE user_external_uid=?`, u).Scan(&postCount)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "connected": true, "handle": integration["handle"], "post_count": postCount, "memory_count": 0, "syncing": integration["syncing"], "last_synced_at": integration["last_synced_at"], "last_sync_source": integration["last_sync_source"]})
}

func (h Handler) XPosts(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !validXPostKind(kind) {
		http.Error(w, "kind must be one of: tweet, bookmark, like", 400)
		return
	}
	limit := 100
	if n, e := strconv.Atoi(r.URL.Query().Get("limit")); e == nil && n > 0 {
		limit = n
	}
	if limit > 500 {
		limit = 500
	}
	if h.DB == nil {
		http.Error(w, "X post storage is not configured", 503)
		return
	}
	query := `SELECT payload FROM x_posts WHERE user_external_uid=?`
	args := []any{u}
	if kind != "" {
		query += ` AND kind=?`
		args = append(args, kind)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := h.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	posts := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) == nil {
			var item map[string]any
			if json.Unmarshal(raw, &item) == nil {
				posts = append(posts, item)
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"posts": posts})
}

func (h Handler) XSync(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	result := h.syncX(r.Context(), u)
	_ = json.NewEncoder(w).Encode(result)
}

// SyncConnectedUsers runs the bounded periodic sweep used by the worker. Each
// account is isolated so a failed X account cannot prevent other accounts from
// being attempted.
func (h Handler) SyncConnectedUsers(ctx context.Context) (users, synced, newPosts int) {
	if h.Service.Client == nil {
		return 0, 0, 0
	}
	nodes, err := h.Service.Client.User.Query().All(ctx)
	if err != nil {
		return 0, 0, 0
	}
	for _, node := range nodes {
		uid := node.ExternalUID
		raw, _ := node.Integrations["x"].(map[string]any)
		connected, _ := raw["connected"].(bool)
		if !connected {
			continue
		}
		users++
		result := h.syncX(ctx, uid)
		if ok, _ := result["success"].(bool); ok {
			synced++
		}
		if count, ok := result["new_posts"].(int); ok {
			newPosts += count
		}
	}
	return users, synced, newPosts
}

func (h Handler) XDisconnect(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if err := h.Service.Save(r.Context(), u, "x", map[string]any{"connected": false, "access_token": "", "refresh_token": "", "syncing": false}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (h Handler) syncX(ctx context.Context, uid string) map[string]any {
	integration, err := h.Service.Raw(ctx, uid, "x")
	if err != nil {
		return map[string]any{"success": false, "error": "not_connected", "new_posts": 0, "memories_created": 0}
	}
	if h.DB == nil {
		return map[string]any{"success": false, "error": "storage_not_configured", "new_posts": 0, "memories_created": 0}
	}
	_ = h.Service.Save(ctx, uid, "x", map[string]any{"syncing": true})
	defer h.Service.Save(ctx, uid, "x", map[string]any{"syncing": false})
	posts := make([]map[string]any, 0)
	source := ""
	if rawToken, _ := integration["access_token"].(string); rawToken != "" {
		if token, tokenErr := h.validXToken(ctx, uid, integration); tokenErr == nil {
			userID, _ := integration["x_user_id"].(string)
			if userID == "" {
				userID, _ = h.fetchXUserID(ctx, token)
				if userID != "" {
					_ = h.Service.Save(ctx, uid, "x", map[string]any{"x_user_id": userID})
				}
			}
			if userID != "" {
				query := url.Values{"max_results": {"100"}, "tweet.fields": {"created_at,lang,public_metrics"}}
				if raw, _, fetchErr := h.fetchX(ctx, token, "/users/"+url.PathEscape(userID)+"/tweets", query); fetchErr == nil {
					for _, item := range raw {
						item["kind"] = "tweet"
						posts = append(posts, item)
					}
					// Optional endpoints are best effort; an authorization failure
					// must not discard tweets already fetched.
					for endpoint, kind := range map[string]string{"/users/" + url.PathEscape(userID) + "/bookmarks": "bookmark", "/users/" + url.PathEscape(userID) + "/liked_tweets": "like"} {
						items, _, optionalErr := h.fetchX(ctx, token, endpoint, query)
						if optionalErr != nil {
							continue
						}
						for _, item := range items {
							item["kind"] = kind
							posts = append(posts, item)
						}
					}
					source = "oauth"
				}
			}
		}
	}
	if source == "" {
		handle, _ := integration["handle"].(string)
		if handle == "" {
			return map[string]any{"success": false, "error": "not_connected", "new_posts": 0, "memories_created": 0}
		}
		if os.Getenv("RAPID_API_KEY") == "" || os.Getenv("RAPID_API_HOST") == "" {
			return map[string]any{"success": false, "error": "rapidapi_not_configured", "new_posts": 0, "memories_created": 0}
		}
		posts, err = h.fetchRapidTimeline(ctx, handle)
		if err != nil {
			return map[string]any{"success": false, "error": "fetch_failed", "new_posts": 0, "memories_created": 0}
		}
		source = "rapidapi"
	}
	newCount := 0
	for _, item := range posts {
		postID := stringValue(item["id"])
		if postID == "" {
			continue
		}
		kind := stringValue(item["kind"])
		if !validXPostKind(kind) {
			kind = "tweet"
		}
		payload := map[string]any{"id": postID, "text": stringValue(item["text"]), "created_at": stringValue(item["created_at"]), "kind": kind, "lang": item["lang"], "metrics": item["public_metrics"]}
		encoded, _ := json.Marshal(payload)
		var createdAt any
		if parsed, e := time.Parse(time.RFC3339, stringValue(item["created_at"])); e == nil {
			createdAt = parsed
		}
		result, e := h.DB.ExecContext(ctx, `INSERT IGNORE INTO x_posts(user_external_uid,post_id,kind,text,created_at,payload,updated_at) VALUES(?,?,?,?,?,?,?)`, uid, postID, kind, stringValue(item["text"]), createdAt, encoded, time.Now().UTC())
		if e == nil {
			n, _ := result.RowsAffected()
			newCount += int(n)
		}
	}
	memoriesCreated := h.extractXMemories(ctx, uid)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_ = h.Service.Save(ctx, uid, "x", map[string]any{"last_synced_at": now, "last_sync_source": source})
	return map[string]any{"success": true, "source": source, "new_posts": newCount, "memories_created": memoriesCreated}
}

func (h Handler) extractXMemories(ctx context.Context, uid string) int {
	if h.Provider == nil || h.DB == nil {
		return 0
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT id,post_id,text FROM x_posts WHERE user_external_uid=? AND memory_extraction_status <> 'completed' ORDER BY created_at ASC LIMIT 200`, uid)
	if err != nil {
		return 0
	}
	type sourcePost struct {
		id           int64
		postID, text string
	}
	posts := make([]sourcePost, 0, 200)
	var parts []string
	for rows.Next() {
		var post sourcePost
		if rows.Scan(&post.id, &post.postID, &post.text) == nil {
			posts = append(posts, post)
			if strings.TrimSpace(post.text) != "" {
				parts = append(parts, post.text)
			}
		}
	}
	_ = rows.Close()
	if len(posts) == 0 || len(parts) == 0 {
		return 0
	}
	joinedPosts := strings.Join(parts, "\n")
	if len([]rune(joinedPosts)) > 12000 {
		joinedPosts = string([]rune(joinedPosts)[:12000])
	}
	prompt := "Extract only durable personal facts, preferences, decisions, or events stated in these X posts. Return JSON only as {\"memories\":[\"...\"]}. Do not invent facts and return an empty array when there is no durable memory.\nPosts:\n" + joinedPosts
	raw, err := h.Provider.Complete(ctx, []chat.Turn{{Role: "system", Content: "You extract concise user memories from social posts."}, {Role: "user", Content: prompt}})
	if err != nil {
		return 0
	}
	var envelope struct {
		Memories []string `json:"memories"`
	}
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		var list []string
		if json.Unmarshal([]byte(raw), &list) != nil {
			return 0
		}
		envelope.Memories = list
	}
	userID := 0
	if h.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE external_uid=?`, uid).Scan(&userID) != nil {
		return 0
	}
	created := 0
	now := time.Now().UTC()
	tags, _ := json.Marshal([]string{"x_connector"})
	for _, value := range envelope.Memories {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len([]rune(value)) > 4000 {
			value = string([]rune(value)[:4000])
		}
		if _, err := h.DB.ExecContext(ctx, `INSERT INTO memories(type,category,visibility,tags,is_read,is_dismissed,content,importance,created_at,updated_at,user_id) VALUES('fact','interesting','private',?,FALSE,FALSE,?,50,?,?,?)`, tags, value, now, now, userID); err == nil {
			created++
		}
	}
	for _, post := range posts {
		_, _ = h.DB.ExecContext(ctx, `UPDATE x_posts SET memory_extraction_status='completed',memory_extracted_at=?,updated_at=? WHERE id=? AND user_external_uid=?`, now, now, post.id, uid)
	}
	return created
}

func (h Handler) fetchRapidTimeline(ctx context.Context, handle string) ([]map[string]any, error) {
	endpoint := "https://" + os.Getenv("RAPID_API_HOST") + "/timeline.php?screenname=" + url.QueryEscape(handle)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-RapidAPI-Key", os.Getenv("RAPID_API_KEY"))
		req.Header.Set("X-RapidAPI-Host", os.Getenv("RAPID_API_HOST"))
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			lastErr = err
		} else {
			var payload struct {
				Status   string           `json:"status"`
				Message  string           `json:"message"`
				Timeline []map[string]any `json:"timeline"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 && decodeErr == nil && payload.Status != "error" {
				posts := make([]map[string]any, 0, len(payload.Timeline))
				for _, item := range payload.Timeline {
					id := stringValue(item["tweet_id"])
					if id == "" {
						continue
					}
					posts = append(posts, map[string]any{"id": id, "text": stringValue(item["text"]), "created_at": stringValue(item["created_at"]), "kind": "tweet"})
				}
				return posts, nil
			}
			lastErr = fmt.Errorf("rapidapi timeline returned %s: %s", resp.Status, payload.Message)
		}
		if attempt < 2 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return nil, lastErr
}

func (h Handler) fetchXUserID(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.x.com/2/users/me?user.fields=username", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", errors.New("X user lookup failed")
	}
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	return stringValue(payload.Data["id"]), nil
}
func (h Handler) fetchX(ctx context.Context, token, path string, query url.Values) ([]map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.x.com/2"+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errors.New("X API request failed")
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, resp.StatusCode, err
	}
	return payload.Data, resp.StatusCode, nil
}
