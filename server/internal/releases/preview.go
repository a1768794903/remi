package releases

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var previewSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var previewSHA40RE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var previewSHA256RE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type previewManifest struct {
	Slug         string `json:"slug"`
	SourceSHA    string `json:"source_sha"`
	DMGURL       string `json:"dmg_url"`
	DMGSHA256    string `json:"dmg_sha256"`
	AppName      string `json:"app_name"`
	BundleID     string `json:"bundle_id"`
	URLScheme    string `json:"url_scheme"`
	BuiltAt      string `json:"built_at"`
	Signer       string `json:"signer"`
	Notarization string `json:"notarization"`
	Notes        string `json:"notes,omitempty"`
	BackendURL   string `json:"backend_url,omitempty"`
}

func previewIdentity(slug string) string {
	digest := sha256.Sum256([]byte(slug))
	return fmt.Sprintf("p%x", digest[:5])
}

func validatePreviewManifest(m previewManifest) error {
	if !previewSlugRE.MatchString(m.Slug) {
		return errors.New("slug must use lowercase letters, digits, and path-safe hyphens")
	}
	if !previewSHA40RE.MatchString(m.SourceSHA) {
		return errors.New("source_sha must be a full 40-character commit SHA")
	}
	if !previewSHA256RE.MatchString(m.DMGSHA256) {
		return errors.New("dmg_sha256 must be a SHA-256 digest")
	}
	id := previewIdentity(m.Slug)
	if !strings.HasPrefix(m.AppName, "Omi Preview") || m.BundleID != "com.omi.preview."+id || m.URLScheme != "omi-preview-"+id {
		return errors.New("preview identity does not match slug")
	}
	if m.Notarization != "stapled" || m.BuiltAt == "" || m.Signer == "" {
		return errors.New("preview build metadata is invalid")
	}
	parsed, err := url.Parse(m.DMGURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "storage.googleapis.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("dmg_url must be an immutable https storage URL")
	}
	expected := "/omi_macos_updates/previews/" + m.Slug + "/" + strings.ToLower(m.SourceSHA) + "/Omi-Preview.dmg"
	if parsed.Path != expected {
		return errors.New("dmg_url must be the canonical immutable preview artifact URL")
	}
	if m.BackendURL != "" {
		backend, err := url.Parse(m.BackendURL)
		if err != nil || backend.Scheme != "https" || backend.Host == "" {
			return errors.New("backend_url must be an https URL")
		}
	}
	return nil
}
