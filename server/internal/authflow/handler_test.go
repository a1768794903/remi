package authflow

import "testing"

func TestValidRedirect(t *testing.T) {
	valid := []string{"omi://auth/callback", "http://localhost:49152/callback", "http://127.0.0.1/callback", "http://[::1]/callback"}
	for _, value := range valid {
		if !validRedirect(value) {
			t.Errorf("valid redirect rejected: %q", value)
		}
	}
	invalid := []string{"https://attacker.example/callback", "http://attacker.example/callback", "javascript://x", "omi://user:pass@host/callback", "omi://x\nheader"}
	for _, value := range invalid {
		if validRedirect(value) {
			t.Errorf("invalid redirect accepted: %q", value)
		}
	}
}

func TestPKCE(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	challenge := pkce(verifier)
	if !validPKCE(challenge, "S256") {
		t.Fatal("generated S256 challenge rejected")
	}
	if validPKCE(challenge, "plain") {
		t.Fatal("plain PKCE accepted")
	}
	if pkce(verifier+"x") == challenge {
		t.Fatal("different verifier produced same challenge")
	}
}
