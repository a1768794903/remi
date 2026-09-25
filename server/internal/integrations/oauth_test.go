package integrations

import (
	"net/url"
	"testing"
)

func TestResolveProviderAliasesAndScopes(t *testing.T) {
	provider, ok := resolveProvider("gmail")
	if !ok || provider.Key != "google_calendar" {
		t.Fatalf("gmail did not resolve to Google Calendar: %#v", provider)
	}
	if provider.Scope != "https://www.googleapis.com/auth/calendar" {
		t.Fatalf("unexpected consent scope: %q", provider.Scope)
	}
}

func TestBuildOAuthURLContainsStateAndProviderParameters(t *testing.T) {
	provider, _ := resolveProvider("google_calendar")
	value, err := buildOAuthURL(provider, "client-id", "https://api.example/v2/integrations/google-calendar/callback", "state-1", "")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "state-1" || query.Get("client_id") != "client-id" || query.Get("scope") == "" {
		t.Fatalf("missing OAuth parameters: %s", value)
	}
}

func TestPKCEChallengeIsDeterministic(t *testing.T) {
	if got := pkceChallenge("verifier"); got != "iMnq5o6zALKXGivsnlom_0F5_WYda32GHkxlV7mq7hQ" {
		t.Fatalf("unexpected PKCE challenge: %q", got)
	}
}
