package mcpkeys

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestPKCES256(t *testing.T) {
	verifier := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !verifyPKCE(verifier, challenge) {
		t.Fatal("valid S256 verifier was rejected")
	}
	if verifyPKCE(verifier+"x", challenge) {
		t.Fatal("invalid S256 verifier was accepted")
	}
}

func TestNormalizeOAuthScopes(t *testing.T) {
	scopes, err := normalizeOAuthScopes("memories.read conversations.read memories.read")
	if err != nil || len(scopes) != 2 {
		t.Fatalf("unexpected scopes: %#v %v", scopes, err)
	}
	if _, err := normalizeOAuthScopes("admin.write"); err == nil {
		t.Fatal("unsupported scope was accepted")
	}
}

func TestValidateRedirect(t *testing.T) {
	if err := validateRedirect("https://client.example/callback"); err != nil {
		t.Fatal(err)
	}
	if err := validateRedirect("http://evil.example/callback"); err == nil {
		t.Fatal("insecure redirect was accepted")
	}
}
