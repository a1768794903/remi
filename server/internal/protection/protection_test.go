package protection

import "testing"

func TestPythonCompatibleAESGCMRoundTrip(t *testing.T) {
	t.Setenv("ENCRYPTION_SECRET", "01234567890123456789012345678901")
	encoded, err := Encrypt("secret text", "firebase-user-1")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decrypt(encoded, "firebase-user-1")
	if err != nil || decoded != "secret text" {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}
	if _, err := Decrypt(encoded, "another-user"); err == nil {
		t.Fatal("wrong UID must not decrypt")
	}
}

func TestProtectionRequiresProductionSecret(t *testing.T) {
	t.Setenv("ENCRYPTION_SECRET", "short")
	if _, err := Encrypt("secret", "uid"); err == nil {
		t.Fatal("short secret should be rejected")
	}
}
