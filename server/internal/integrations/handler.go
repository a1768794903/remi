package integrations

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"strings"
	"time"
)

type Handler struct {
	Service  Service
	DB       *sql.DB
	Provider chat.Provider
}

type ConnectorSynthesisTask struct {
	Description string `json:"description"`
	Priority    string `json:"priority,omitempty"`
	DueAt       string `json:"due_at,omitempty"`
}

type connectorSynthesisRequest struct {
	Source           string   `json:"source"`
	Items            []string `json:"items"`
	ExistingMemories []string `json:"existing_memories,omitempty"`
}

type connectorSynthesisResponse struct {
	Memories []string                 `json:"memories"`
	Tasks    []ConnectorSynthesisTask `json:"tasks"`
	Profile  string                   `json:"profile,omitempty"`
}

func (h Handler) Synthesize(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in connectorSynthesisRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in) != nil || strings.TrimSpace(in.Source) == "" || len(in.Items) == 0 || len(in.Items) > 100 {
		http.Error(w, "source and items are required", http.StatusUnprocessableEntity)
		return
	}
	if h.Provider == nil {
		http.Error(w, "connector synthesis provider is not configured", http.StatusServiceUnavailable)
		return
	}
	prompt, _ := json.Marshal(map[string]any{"source": in.Source, "items": in.Items, "existing_memories": in.ExistingMemories})
	answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Return only JSON with memories (array of strings), tasks (array of objects with description, priority, due_at), and profile (string)."}, {Role: "user", Content: string(prompt)}})
	if err != nil {
		http.Error(w, "connector synthesis failed", http.StatusBadGateway)
		return
	}
	var out connectorSynthesisResponse
	if json.Unmarshal([]byte(answer), &out) != nil {
		http.Error(w, "connector synthesis returned invalid JSON", http.StatusBadGateway)
		return
	}
	for i := range out.Tasks {
		if strings.TrimSpace(out.Tasks[i].Description) == "" || len(out.Tasks[i].Description) > 1000 {
			http.Error(w, "connector synthesis returned invalid task", http.StatusBadGateway)
			return
		}
	}
	_ = uid // identity is deliberately resolved before provider dispatch for audit parity.
	_ = json.NewEncoder(w).Encode(out)
}

func (h Handler) uid(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, e := auth.UserID(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 401)
		return "", false
	}
	return u, true
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	key := r.PathValue("app_key")
	switch r.Method {
	case http.MethodGet:
		v, e := h.Service.Get(r.Context(), u, key)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	case http.MethodPut:
		var in map[string]any
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if e := h.Service.Save(r.Context(), u, key, in); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "app_key": key})
	case http.MethodDelete:
		if e := h.Service.Delete(r.Context(), u, key); errors.Is(e, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}
func (h Handler) AppleHealth(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	v, e := h.Service.AppleHealth(r.Context(), u, in)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) OAuthURL(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	p, exists := resolveProvider(r.PathValue("app_key"))
	if !exists {
		tp, taskExists := taskProvider(r.PathValue("app_key"))
		if !taskExists {
			http.Error(w, "unsupported integration", http.StatusBadRequest)
			return
		}
		baseURL := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
		clientID := taskClientID(tp.Key)
		if baseURL == "" || clientID == "" || h.Service.Redis == nil {
			http.Error(w, "task integration OAuth provider is not configured", http.StatusServiceUnavailable)
			return
		}
		state, err := randomToken(32)
		if err != nil {
			http.Error(w, "could not create OAuth state", 500)
			return
		}
		stateValue, _ := json.Marshal(map[string]string{"uid": u, "app_key": tp.Key})
		if err := h.Service.Redis.Set(r.Context(), "remi:oauth-state:"+state, stateValue, 10*time.Minute).Err(); err != nil {
			http.Error(w, "could not store OAuth state", 503)
			return
		}
		authURL, err := buildTaskOAuthURL(tp, clientID, baseURL+tp.RedirectPath, state)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"auth_url": authURL})
		return
	}
	if !exists {
		http.Error(w, "unsupported integration", http.StatusBadRequest)
		return
	}
	baseURL := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if baseURL == "" || clientID == "" || h.Service.Redis == nil {
		http.Error(w, "integration OAuth provider is not configured", http.StatusServiceUnavailable)
		return
	}
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "could not create OAuth state", 500)
		return
	}
	stateValue, _ := json.Marshal(map[string]string{"uid": u, "app_key": p.Key})
	if err := h.Service.Redis.Set(r.Context(), "remi:oauth-state:"+state, stateValue, 10*time.Minute).Err(); err != nil {
		http.Error(w, "could not store OAuth state", http.StatusServiceUnavailable)
		return
	}
	redirectURI := baseURL + p.RedirectPath
	authURL, err := buildOAuthURL(p, clientID, redirectURI, state, "")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"auth_url": authURL})
}

func (h Handler) TaskIntegrations(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	integrations, def, err := h.Service.TaskIntegrations(r.Context(), u)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"integrations": integrations, "default_app": def})
}

func (h Handler) DefaultTaskIntegration(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		_, def, err := h.Service.TaskIntegrations(r.Context(), u)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"default_app": def})
		return
	}
	var in struct {
		AppKey string `json:"app_key"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if err := h.Service.SetDefaultTaskIntegration(r.Context(), u, in.AppKey); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"default_app": in.AppKey})
}

func (h Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	var in map[string]any
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if _, ok := in["title"].(string); !ok || strings.TrimSpace(in["title"].(string)) == "" {
		http.Error(w, "title is required", 400)
		return
	}
	result, err := h.Service.CreateExternalTask(r.Context(), u, r.PathValue("app_key"), in)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (h Handler) TaskCatalog(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	path := r.URL.Path
	key := ""
	endpoint := ""
	authPrefix := "Bearer "
	switch {
	case strings.Contains(path, "/asana/workspaces"):
		key, endpoint = "asana", "https://app.asana.com/api/1.0/workspaces"
	case strings.Contains(path, "/asana/projects/"):
		key, endpoint = "asana", "https://app.asana.com/api/1.0/projects?workspace="+url.PathEscape(r.PathValue("workspace_gid"))+"&archived=false&opt_fields=name,gid,owner&limit=100"
	case strings.Contains(path, "/clickup/teams"):
		key, endpoint, authPrefix = "clickup", "https://api.clickup.com/api/v2/team", ""
	case strings.Contains(path, "/clickup/spaces/"):
		key, endpoint, authPrefix = "clickup", "https://api.clickup.com/api/v2/team/"+url.PathEscape(r.PathValue("team_id"))+"/space?archived=false", ""
	case strings.Contains(path, "/clickup/lists/"):
		key, endpoint, authPrefix = "clickup", "https://api.clickup.com/api/v2/space/"+url.PathEscape(r.PathValue("space_id"))+"/list?archived=false", ""
	default:
		http.Error(w, "unsupported task catalog", 400)
		return
	}
	integration, err := h.Service.Raw(r.Context(), u, key)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	token, _ := integration["access_token"].(string)
	if token == "" {
		http.Error(w, "not authenticated", 401)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	req.Header.Set("Authorization", authPrefix+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	if task, ok := taskProvider(r.PathValue("app_key")); ok {
		h.taskOAuthCallback(w, r, task)
		return
	}
	requested, _ := resolveProvider(r.PathValue("app_key"))
	if requested.Key == "" {
		oauthHTML(w, http.StatusBadRequest, "config_error")
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" || h.Service.Redis == nil {
		oauthHTML(w, http.StatusBadRequest, "missing_code")
		return
	}
	rawState, err := h.Service.Redis.GetDel(r.Context(), "remi:oauth-state:"+state).Result()
	if err != nil {
		oauthHTML(w, http.StatusBadRequest, "invalid_state")
		return
	}
	var stateData struct {
		UID    string `json:"uid"`
		AppKey string `json:"app_key"`
	}
	if json.Unmarshal([]byte(rawState), &stateData) != nil || stateData.UID == "" || stateData.AppKey != requested.Key {
		oauthHTML(w, http.StatusBadRequest, "invalid_state")
		return
	}
	baseURL := strings.TrimRight(os.Getenv("BASE_API_URL"), "/")
	clientID, clientSecret := os.Getenv("GOOGLE_CLIENT_ID"), os.Getenv("GOOGLE_CLIENT_SECRET")
	if baseURL == "" || clientID == "" || clientSecret == "" {
		oauthHTML(w, http.StatusBadRequest, "config_error")
		return
	}
	form := url.Values{"code": {code}, "client_id": {clientID}, "client_secret": {clientSecret}, "redirect_uri": {baseURL + requested.RedirectPath}, "grant_type": {"authorization_code"}}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, requested.TokenEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		oauthHTML(w, 502, "server_error")
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		oauthHTML(w, 502, "server_error")
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		oauthHTML(w, 502, "server_error")
		return
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if json.Unmarshal(body, &token) != nil || token.AccessToken == "" {
		oauthHTML(w, 502, "server_error")
		return
	}
	integration := map[string]any{"connected": true, "access_token": token.AccessToken, "granted_scopes": strings.Fields(token.Scope)}
	if token.RefreshToken != "" {
		integration["refresh_token"] = token.RefreshToken
	}
	if err := h.Service.Save(r.Context(), stateData.UID, requested.Key, integration); err != nil {
		oauthHTML(w, 500, "server_error")
		return
	}
	oauthHTML(w, http.StatusOK, "success")
}

func (h Handler) taskOAuthCallback(w http.ResponseWriter, r *http.Request, p taskProviderConfig) {
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if state == "" || code == "" || h.Service.Redis == nil {
		oauthHTML(w, 400, "missing_code")
		return
	}
	raw, err := h.Service.Redis.GetDel(r.Context(), "remi:oauth-state:"+state).Result()
	if err != nil {
		oauthHTML(w, 400, "invalid_state")
		return
	}
	var stateData struct {
		UID    string `json:"uid"`
		AppKey string `json:"app_key"`
	}
	if json.Unmarshal([]byte(raw), &stateData) != nil || stateData.UID == "" || stateData.AppKey != p.Key {
		oauthHTML(w, 400, "invalid_state")
		return
	}
	baseURL, clientID, clientSecret := strings.TrimRight(os.Getenv("BASE_API_URL"), "/"), taskClientID(p.Key), os.Getenv(strings.ToUpper(strings.ReplaceAll(p.Key, "-", "_"))+"_CLIENT_SECRET")
	if baseURL == "" || clientID == "" || clientSecret == "" {
		oauthHTML(w, 400, "config_error")
		return
	}
	form := url.Values{"code": {code}, "client_id": {clientID}, "client_secret": {clientSecret}, "redirect_uri": {baseURL + p.RedirectPath}, "grant_type": {"authorization_code"}}
	if p.Key == "clickup" {
		form.Del("redirect_uri")
		form.Del("grant_type")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.TokenEndpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		oauthHTML(w, 502, "server_error")
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		oauthHTML(w, 502, "server_error")
		return
	}
	defer resp.Body.Close()
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || json.NewDecoder(resp.Body).Decode(&token) != nil || token.AccessToken == "" {
		oauthHTML(w, 502, "server_error")
		return
	}
	data := map[string]any{"connected": true, "access_token": token.AccessToken, "granted_scopes": strings.Fields(token.Scope)}
	if token.RefreshToken != "" {
		data["refresh_token"] = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		data["expires_at"] = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if err := h.Service.Save(r.Context(), stateData.UID, p.Key, data); err != nil {
		oauthHTML(w, 500, "server_error")
		return
	}
	oauthHTML(w, 200, "success")
}

func randomToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func oauthHTML(w http.ResponseWriter, status int, result string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<html><body><h1>OAuth "+html.EscapeString(result)+"</h1></body></html>")
}
func (h Handler) CalendarStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	raw, e := h.Service.Raw(r.Context(), u, "google_calendar")
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	connected, _ := raw["connected"].(bool)
	skipped, _ := raw["onboarding_skipped"].(bool)
	reauth, _ := raw["reauth_required"].(bool)
	_, hasToken := raw["access_token"]
	needs := reauth || (connected && !hasToken)
	state := "not_started"
	if needs {
		state = "needs_reconnect"
	} else if connected {
		state = "connected"
	} else if skipped {
		state = "skipped"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"connected": connected, "onboarding_completed": connected || skipped, "needs_reconnect": needs, "reauth_reason": raw["reauth_reason"], "state": state})
}
func (h Handler) CalendarSkip(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if e := h.Service.Save(r.Context(), u, "google_calendar", map[string]any{"onboarding_skipped": true}); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"skipped": true})
}
func (h Handler) CalendarReset(w http.ResponseWriter, r *http.Request) {
	u, ok := h.uid(w, r)
	if !ok {
		return
	}
	if e := h.Service.Save(r.Context(), u, "google_calendar", map[string]any{"onboarding_skipped": false, "reauth_required": false, "reauth_reason": nil}); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"reset": true})
}
