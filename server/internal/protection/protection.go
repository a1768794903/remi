package protection

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/hkdf"
)

var ErrKeyUnavailable = errors.New("ENCRYPTION_SECRET must be at least 32 bytes")

func key(uid string) ([]byte, error) {
	secret := []byte(os.Getenv("ENCRYPTION_SECRET"))
	if len(secret) < 32 || strings.TrimSpace(uid) == "" {
		return nil, ErrKeyUnavailable
	}
	reader := hkdf.New(sha256.New, secret, []byte(uid), []byte("user-data-encryption"))
	derived := make([]byte, 32)
	if _, err := io.ReadFull(reader, derived); err != nil {
		return nil, err
	}
	return derived, nil
}

func Encrypt(plaintext, uid string) (string, error) {
	if plaintext == "" {
		return plaintext, nil
	}
	k, err := key(uid)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	payload := append(nonce, gcm.Seal(nil, nonce, []byte(plaintext), nil)...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func Decrypt(encoded, uid string) (string, error) {
	if encoded == "" {
		return encoded, nil
	}
	k, err := key(uid)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted payload")
	}
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
