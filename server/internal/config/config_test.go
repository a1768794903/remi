package config

import (
	"testing"
	"time"
)

func TestFromEnvUsesRemiDefaults(t *testing.T) {
	for _, key := range []string{"HTTP_ADDR", "CONVERSATION_GAP_MINUTES", "AUTH_MODE"} {
		t.Setenv(key, "")
	}
	cfg := FromEnv()
	if cfg.HTTPAddr != ":8080" || cfg.AuthMode != "dev" || cfg.ConversationGap != 10*time.Minute {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestFromEnvReadsOverrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("CONVERSATION_GAP_MINUTES", "15")
	t.Setenv("AUTH_MODE", "firebase")
	cfg := FromEnv()
	if cfg.HTTPAddr != ":9090" || cfg.AuthMode != "firebase" || cfg.ConversationGap != 15*time.Minute {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
}
