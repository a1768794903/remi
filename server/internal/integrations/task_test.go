package integrations

import (
	"net/url"
	"strings"
	"testing"
)

func TestTaskProviderOAuthURL(t *testing.T) {
	tests := []struct {
		key       string
		clientID  string
		wantHost  string
		wantPath  string
		wantScope string
	}{
		{"todoist", "todo-client", "todoist.com", "/oauth/authorize", "data:read_write"},
		{"asana", "asana-client", "app.asana.com", "/-/oauth_authorize", "tasks:read tasks:write workspaces:read projects:read users:read"},
		{"google_tasks", "google-client", "accounts.google.com", "/o/oauth2/v2/auth", "https://www.googleapis.com/auth/tasks"},
		{"clickup", "click-client", "app.clickup.com", "/api", ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			p, ok := taskProvider(tt.key)
			if !ok {
				t.Fatalf("provider not found")
			}
			raw, err := buildTaskOAuthURL(p, tt.clientID, "https://api.example.test/v2/integrations/"+tt.key+"/callback", "state-token")
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != tt.wantHost || u.Path != tt.wantPath {
				t.Fatalf("url = %s", raw)
			}
			if got := u.Query().Get("client_id"); got != tt.clientID {
				t.Fatalf("client_id = %q", got)
			}
			if got := u.Query().Get("state"); got != "state-token" {
				t.Fatalf("state = %q", got)
			}
			if got := u.Query().Get("scope"); got != tt.wantScope {
				t.Fatalf("scope = %q, want %q", got, tt.wantScope)
			}
		})
	}
}

func TestTaskRequestPayloads(t *testing.T) {
	date := "2026-09-25T10:20:30Z"
	for _, key := range []string{"todoist", "asana", "google_tasks", "clickup"} {
		payload, err := taskPayload(key, map[string]any{"title": "Buy milk", "description": "2%", "due_date": date}, map[string]any{
			"workspace_gid": "workspace", "project_gid": "project", "user_gid": "user", "default_list_id": "list", "list_id": "list",
		})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if !strings.Contains(string(payload), "Buy milk") {
			t.Fatalf("%s payload lacks title: %s", key, payload)
		}
	}
}
