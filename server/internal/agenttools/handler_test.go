package agenttools

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCoreToolsExposeProviderBackedTools(t *testing.T) {
	seen := map[string]bool{}
	for _, item := range coreTools {
		seen[item.Name] = true
	}
	for _, name := range []string{"search_action_items", "create_action_item", "search_memories", "get_calendar_events_tool", "create_calendar_event_tool", "update_calendar_event_tool", "delete_calendar_event_tool", "get_gmail_messages_tool"} {
		if !seen[name] {
			t.Fatalf("missing core tool %q", name)
		}
	}
}

func TestProviderToolSchemasMatchPythonCalendarAndGmailArguments(t *testing.T) {
	byName := map[string]tool{}
	for _, item := range coreTools {
		byName[item.Name] = item
	}
	if required := byName["create_calendar_event_tool"].Parameters["required"].([]string); len(required) != 3 {
		t.Fatalf("create calendar tool must require title and start/end times, got %#v", required)
	}
	if _, ok := byName["get_gmail_messages_tool"].Parameters["properties"].(map[string]any)["label"]; !ok {
		t.Fatal("gmail tool must expose label filtering")
	}
}

func TestParseGmailMessageReturnsReadableHeadersAndPlainBody(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte("Hello from Gmail"))
	parsed := parseGmailMessage(map[string]any{
		"id": "m-1", "threadId": "t-1", "snippet": "Hello preview",
		"payload": map[string]any{
			"mimeType": "text/plain",
			"headers": []any{
				map[string]any{"name": "Subject", "value": "Project update"},
				map[string]any{"name": "From", "value": "Alice <alice@example.com>"},
				map[string]any{"name": "To", "value": "Bob <bob@example.com>"},
				map[string]any{"name": "Date", "value": "Mon, 02 Jan 2006 15:04:05 +0000"},
			},
			"body": map[string]any{"data": body},
		},
	})
	if parsed["subject"] != "Project update" || parsed["from"] != "Alice <alice@example.com>" || parsed["body"] != "Hello from Gmail" {
		t.Fatalf("unexpected parsed message: %#v", parsed)
	}
	if !strings.Contains(parsed["date"].(string), "2006-01-02T15:04:05") {
		t.Fatalf("expected normalized date, got %#v", parsed["date"])
	}
}

func TestParseGmailMessagePrefersPlainTextNestedPart(t *testing.T) {
	plain := base64.RawURLEncoding.EncodeToString([]byte("plain"))
	html := base64.RawURLEncoding.EncodeToString([]byte("<b>html</b>"))
	parsed := parseGmailMessage(map[string]any{"payload": map[string]any{
		"parts": []any{
			map[string]any{"mimeType": "multipart/alternative", "parts": []any{
				map[string]any{"mimeType": "text/html", "body": map[string]any{"data": html}},
				map[string]any{"mimeType": "text/plain", "body": map[string]any{"data": plain}},
			}},
		},
	}})
	if parsed["body"] != "plain" {
		t.Fatalf("expected nested plain text body, got %#v", parsed["body"])
	}
}

func TestGoogleIntegrationHasGmailScopeOnlyWhenScopesAreRecorded(t *testing.T) {
	if !googleIntegrationHasGmailScope(map[string]any{}) {
		t.Fatal("legacy integrations without persisted scopes should remain usable")
	}
	if googleIntegrationHasGmailScope(map[string]any{"scopes": []any{"https://www.googleapis.com/auth/calendar.readonly"}}) {
		t.Fatal("calendar-only grant must not be treated as Gmail-enabled")
	}
	if !googleIntegrationHasGmailScope(map[string]any{"scopes": []any{"https://www.googleapis.com/auth/gmail.readonly"}}) {
		t.Fatal("Gmail readonly grant should be accepted")
	}
}

func TestSelectCalendarEventIDByTitleRequiresExactlyOneMatch(t *testing.T) {
	events := map[string]any{"items": []any{
		map[string]any{"id": "one", "summary": "Planning"},
		map[string]any{"id": "two", "summary": "Other"},
	}}
	if got, err := selectCalendarEventID(events, "planning"); err != nil || got != "one" {
		t.Fatalf("expected one matching event, got %q, %v", got, err)
	}
	if _, err := selectCalendarEventID(events, "missing"); err == nil {
		t.Fatal("expected missing event error")
	}
	duplicates := map[string]any{"items": []any{
		map[string]any{"id": "one", "summary": "Planning"},
		map[string]any{"id": "two", "summary": "Planning later"},
	}}
	if _, err := selectCalendarEventID(duplicates, "planning"); err == nil {
		t.Fatal("expected multiple event error")
	}
}

func TestObjectSchemaDoesNotInventOptionalRequiredFields(t *testing.T) {
	schema := objectSchema(map[string]any{"query": map[string]any{"type": "string"}}, "query")
	if len(schema["required"].([]string)) != 1 || schema["required"].([]string)[0] != "query" {
		t.Fatalf("unexpected required fields: %#v", schema["required"])
	}
}
