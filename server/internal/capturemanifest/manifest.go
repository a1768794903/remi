package capturemanifest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const TTL = 15 * time.Minute

type Claim struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type payload struct {
	Version      int     `json:"v"`
	UID          string  `json:"uid"`
	Device       string  `json:"device"`
	Conversation string  `json:"conversation"`
	Files        []Claim `json:"files"`
	IssuedAt     int64   `json:"iat"`
	ExpiresAt    int64   `json:"exp"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func normalize(claims []Claim) ([]Claim, error) {
	if len(claims) == 0 {
		return nil, errors.New("capture manifest requires at least one file")
	}
	out := make([]Claim, 0, len(claims))
	for _, claim := range claims {
		if claim.Name == "" || filepath.Base(claim.Name) != claim.Name || !digestPattern.MatchString(claim.SHA256) {
			return nil, errors.New("invalid capture manifest file claim")
		}
		claim.SHA256 = stringLower(claim.SHA256)
		out = append(out, claim)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].SHA256 < out[j].SHA256
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func stringLower(value string) string {
	return strings.ToLower(value)
}

func secret() ([]byte, error) {
	value := os.Getenv("SYNC_CONTENT_ID_SECRET")
	if value == "" {
		value = os.Getenv("ENCRYPTION_SECRET")
	}
	if value == "" {
		return nil, errors.New("SYNC_CONTENT_ID_SECRET or ENCRYPTION_SECRET is required")
	}
	return []byte(value), nil
}

func Issue(uid, device, conversation string, claims []Claim, now time.Time) (string, error) {
	key, err := secret()
	if err != nil {
		return "", err
	}
	claims, err = normalize(claims)
	if err != nil {
		return "", err
	}
	p := payload{Version: 1, UID: uid, Device: device, Conversation: conversation, Files: claims, IssuedAt: now.Unix(), ExpiresAt: now.Add(TTL).Unix()}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(encoded))
	return encoded + "." + hex.EncodeToString(h.Sum(nil)), nil
}

func Verify(token, uid, device, conversation string, filenames []string, now time.Time) ([]Claim, bool) {
	key, err := secret()
	if err != nil {
		return nil, false
	}
	var encoded, signature string
	for i, c := range token {
		if c == '.' {
			encoded, signature = token[:i], token[i+1:]
			break
		}
	}
	if encoded == "" || signature == "" {
		return nil, false
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(encoded))
	expected := hex.EncodeToString(h.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	var p payload
	if json.Unmarshal(raw, &p) != nil || p.Version != 1 || p.UID != uid || p.Device != device || p.Conversation != conversation || now.Unix() > p.ExpiresAt || p.IssuedAt > now.Unix()+60 {
		return nil, false
	}
	claims, err := normalize(p.Files)
	if err != nil {
		return nil, false
	}
	names := make([]string, 0, len(filenames))
	for _, name := range filenames {
		names = append(names, filepath.Base(name))
	}
	sort.Strings(names)
	if len(names) != len(claims) {
		return nil, false
	}
	for i := range names {
		if names[i] != claims[i].Name {
			return nil, false
		}
	}
	return claims, true
}

func ClaimsMatchBytes(claims []Claim, files map[string][]byte) bool {
	normalized, err := normalize(claims)
	if err != nil || len(normalized) != len(files) {
		return false
	}
	for _, claim := range normalized {
		data, ok := files[claim.Name]
		if !ok {
			return false
		}
		hash := sha256.Sum256(data)
		if !hmac.Equal([]byte(hex.EncodeToString(hash[:])), []byte(claim.SHA256)) {
			return false
		}
	}
	return true
}

func ClaimsMatchReaders(claims []Claim, files map[string]io.Reader) bool {
	contents := make(map[string][]byte, len(files))
	for name, reader := range files {
		data, err := io.ReadAll(reader)
		if err != nil {
			return false
		}
		contents[name] = data
	}
	return ClaimsMatchBytes(claims, contents)
}

func Fingerprint(claims []Claim) (string, error) {
	normalized, err := normalize(claims)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
