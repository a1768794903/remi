package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPProviderComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("missing auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()
	answer, err := (HTTPProvider{Endpoint: server.URL, APIKey: "secret", Model: "test"}).Complete(context.Background(), []Turn{{Role: "user", Content: "hi"}})
	if err != nil || answer != "hello" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestHTTPProviderRejectsEmptyAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }))
	defer server.Close()
	_, err := (HTTPProvider{Endpoint: server.URL}).Complete(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "no answer") {
		t.Fatalf("unexpected err=%v", err)
	}
}

func TestHTTPProviderReportsUsageAndCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1000000,"completion_tokens":1000000,"cached_tokens":0}}`))
	}))
	defer server.Close()
	answer, usage, err := (HTTPProvider{Endpoint: server.URL, Model: "gpt-4o-mini"}).CompleteWithUsage(context.Background(), nil)
	if err != nil || answer != "ok" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if usage.InputTokens != 1000000 || usage.OutputTokens != 1000000 || usage.CostMicroUSD != 750000 {
		t.Fatalf("unexpected usage=%+v", usage)
	}
}

func TestNullProviderFailsClosed(t *testing.T) {
	if _, err := (NullProvider{}).Complete(context.Background(), nil); err == nil {
		t.Fatal("expected provider configuration error")
	}
}
