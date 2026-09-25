package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type taskProviderConfig struct {
	Key, AuthBase, TokenEndpoint, Scope, RedirectPath string
}

func taskProvider(key string) (taskProviderConfig, bool) {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(key, "-", "_"))) {
	case "todoist":
		return taskProviderConfig{"todoist", "https://todoist.com/oauth/authorize", "https://todoist.com/oauth/access_token", "data:read_write", "/v2/integrations/todoist/callback"}, true
	case "asana":
		return taskProviderConfig{"asana", "https://app.asana.com/-/oauth_authorize", "https://app.asana.com/-/oauth_token", "tasks:read tasks:write workspaces:read projects:read users:read", "/v2/integrations/asana/callback"}, true
	case "google_tasks":
		return taskProviderConfig{"google_tasks", "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://www.googleapis.com/auth/tasks", "/v2/integrations/google-tasks/callback"}, true
	case "clickup":
		return taskProviderConfig{"clickup", "https://app.clickup.com/api", "https://api.clickup.com/api/v2/oauth/token", "", "/v2/integrations/clickup/callback"}, true
	default:
		return taskProviderConfig{}, false
	}
}

func buildTaskOAuthURL(p taskProviderConfig, clientID, redirectURI, state string) (string, error) {
	if clientID == "" || redirectURI == "" || state == "" {
		return "", errors.New("task OAuth configuration is incomplete")
	}
	q := url.Values{"client_id": {clientID}, "state": {state}, "redirect_uri": {redirectURI}}
	switch p.Key {
	case "todoist":
		q.Set("scope", p.Scope)
	case "asana":
		q.Set("response_type", "code")
		q.Set("scope", p.Scope)
	case "google_tasks":
		q.Set("response_type", "code")
		q.Set("scope", p.Scope)
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
	return p.AuthBase + "?" + q.Encode(), nil
}

func taskPayload(key string, in map[string]any, integration map[string]any) ([]byte, error) {
	title, _ := in["title"].(string)
	description, _ := in["description"].(string)
	due, _ := in["due_date"].(string)
	switch key {
	case "todoist":
		v := map[string]any{"content": title, "priority": 2}
		if description != "" {
			v["description"] = description
		}
		if due != "" {
			v["due_string"] = due[:min(len(due), 10)]
		}
		return json.Marshal(v)
	case "asana":
		v := map[string]any{"name": title, "workspace": integration["workspace_gid"]}
		if description != "" {
			v["notes"] = description
		}
		if due != "" {
			v["due_on"] = due[:min(len(due), 10)]
		}
		if x := integration["user_gid"]; x != nil {
			v["assignee"] = x
		}
		if x := integration["project_gid"]; x != nil {
			v["projects"] = []any{x}
		}
		return json.Marshal(map[string]any{"data": v})
	case "google_tasks":
		v := map[string]any{"title": title}
		if description != "" {
			v["notes"] = description
		}
		if due != "" {
			v["due"] = due
		}
		return json.Marshal(v)
	case "clickup":
		v := map[string]any{"name": title}
		if description != "" {
			v["description"] = description
		}
		if due != "" {
			if t, e := time.Parse(time.RFC3339, due); e == nil {
				v["due_date"] = t.UnixMilli()
			}
		}
		return json.Marshal(v)
	default:
		return nil, fmt.Errorf("unsupported task integration: %s", key)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s Service) TaskIntegrations(ctx context.Context, uid string) (map[string]any, string, error) {
	u, err := s.owner(ctx, uid)
	if err != nil {
		return nil, "", err
	}
	result := map[string]any{}
	def := ""
	for k, v := range u.Integrations {
		if k == "_default_task_integration" {
			if m, ok := v.(map[string]any); ok {
				def, _ = m["value"].(string)
			} else {
				def, _ = v.(string)
			}
			continue
		}
		if _, ok := taskProvider(k); ok {
			result[k] = v
		}
	}
	return result, def, nil
}

func (s Service) SetDefaultTaskIntegration(ctx context.Context, uid, key string) error {
	if key != "" {
		if _, ok := taskProvider(key); !ok {
			return errors.New("unsupported task integration")
		}
	}
	return s.Save(ctx, uid, "_default_task_integration", map[string]any{"value": key})
}

func (s Service) CreateExternalTask(ctx context.Context, uid, key string, in map[string]any) (map[string]any, error) {
	if _, ok := taskProvider(key); !ok {
		return nil, errors.New("unsupported task integration")
	}
	integration, err := s.Raw(ctx, uid, key)
	if err != nil {
		return nil, err
	}
	if connected, _ := integration["connected"].(bool); !connected {
		return nil, errors.New("task integration is not connected")
	}
	integration, err = s.ensureTaskToken(ctx, uid, key, integration)
	if err != nil {
		return nil, err
	}
	token, _ := integration["access_token"].(string)
	if token == "" {
		return nil, errors.New("no access token")
	}
	payload, err := taskPayload(key, in, integration)
	if err != nil {
		return nil, err
	}
	endpoint := map[string]string{"todoist": "https://api.todoist.com/api/v1/tasks", "asana": "https://app.asana.com/api/1.0/tasks", "google_tasks": "https://tasks.googleapis.com/tasks/v1/lists/" + fmt.Sprint(integration["default_list_id"]) + "/tasks", "clickup": "https://api.clickup.com/api/v2/list/" + fmt.Sprint(integration["list_id"]) + "/task"}[key]
	if (key == "asana" && integration["workspace_gid"] == nil) || (key == "google_tasks" && integration["default_list_id"] == nil) || (key == "clickup" && integration["list_id"] == nil) {
		return nil, errors.New("task destination is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if key == "clickup" {
		req.Header.Set("Authorization", token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && (key == "asana" || key == "google_tasks") {
		resp.Body.Close()
		integration, err = s.refreshTaskToken(ctx, uid, key, integration)
		if err != nil {
			return nil, err
		}
		newToken, _ := integration["access_token"].(string)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+newToken)
		resp, err = (&http.Client{Timeout: 20 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return map[string]any{"success": false, "error": fmt.Sprintf("%s API error: %d", key, resp.StatusCode), "error_code": "api_error"}, nil
	}
	id := body["id"]
	if key == "asana" {
		if d, ok := body["data"].(map[string]any); ok {
			id = d["gid"]
		}
	}
	return map[string]any{"success": true, "external_task_id": fmt.Sprint(id)}, nil
}

func (s Service) ensureTaskToken(ctx context.Context, uid, key string, integration map[string]any) (map[string]any, error) {
	if key != "asana" && key != "google_tasks" {
		return integration, nil
	}
	refresh, _ := integration["refresh_token"].(string)
	if refresh == "" {
		return integration, nil
	}
	need := key == "google_tasks"
	if raw, ok := integration["expires_at"].(string); ok && raw != "" {
		if expiry, err := time.Parse(time.RFC3339, raw); err == nil {
			need = time.Now().UTC().Add(5 * time.Minute).After(expiry)
		} else {
			need = true
		}
	}
	if !need {
		return integration, nil
	}
	return s.refreshTaskToken(ctx, uid, key, integration)
}

func (s Service) refreshTaskToken(ctx context.Context, uid, key string, integration map[string]any) (map[string]any, error) {
	refresh, _ := integration["refresh_token"].(string)
	if refresh == "" {
		return nil, errors.New("no refresh token available")
	}
	clientID, secret := taskClientID(key), os.Getenv(strings.ToUpper(strings.ReplaceAll(key, "-", "_"))+"_CLIENT_SECRET")
	if clientID == "" || secret == "" {
		return nil, errors.New("task OAuth provider is not configured")
	}
	endpoint := "https://oauth2.googleapis.com/token"
	if key == "asana" {
		endpoint = "https://app.asana.com/-/oauth_token"
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "refresh_token": {refresh}, "grant_type": {"refresh_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s token refresh failed", key)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if json.Unmarshal(raw, &token) != nil || token.AccessToken == "" {
		return nil, errors.New("invalid refreshed access token")
	}
	update := map[string]any{"connected": true, "access_token": token.AccessToken}
	if token.RefreshToken != "" {
		update["refresh_token"] = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		update["expires_at"] = time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if err := s.Save(ctx, uid, key, update); err != nil {
		return nil, err
	}
	out := clone(integration)
	for k, v := range update {
		out[k] = v
	}
	return out, nil
}

func taskClientID(key string) string {
	return os.Getenv(strings.ToUpper(strings.ReplaceAll(key, "-", "_")) + "_CLIENT_ID")
}
