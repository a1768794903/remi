package releases

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type releaseManifest struct {
	ReleaseID           string `json:"release_id"`
	Platform            string `json:"platform"`
	Channel             string `json:"channel"`
	Version             string `json:"version"`
	BuildNumber         int    `json:"build_number"`
	ZipURL              string `json:"zip_url"`
	DMGURL              string `json:"dmg_url"`
	EDSignature         string `json:"ed_signature"`
	AppSourceSHA        string `json:"app_source_sha"`
	QualificationTier   string `json:"qualification_tier"`
	QualificationPassed bool   `json:"qualification_passed"`
}

func validateReleaseManifest(manifest releaseManifest) error {
	for name, value := range map[string]string{
		"release_id": manifest.ReleaseID, "version": manifest.Version,
		"zip_url": manifest.ZipURL, "ed_signature": manifest.EDSignature,
		"app_source_sha": manifest.AppSourceSHA,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if manifest.Platform != "macos" && manifest.Platform != "windows" && manifest.Platform != "linux" {
		return errors.New("invalid platform")
	}
	if manifest.Channel != "stable" && manifest.Channel != "beta" {
		return errors.New("invalid channel")
	}
	if manifest.BuildNumber < 1 {
		return errors.New("build_number must be positive")
	}
	if err := httpsGitHubAsset(manifest.ZipURL); err != nil {
		return fmt.Errorf("zip_url: %w", err)
	}
	if manifest.Platform == "macos" {
		if strings.TrimSpace(manifest.DMGURL) == "" {
			return errors.New("dmg_url is required for macos")
		}
		if err := httpsGitHubAsset(manifest.DMGURL); err != nil {
			return fmt.Errorf("dmg_url: %w", err)
		}
	}
	if manifest.QualificationTier != "T2" && manifest.QualificationTier != "signed-smoke" {
		return errors.New("release manifest qualification is missing accepted normal-path evidence")
	}
	return nil
}

func httpsGitHubAsset(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" {
		return errors.New("must be a github.com release asset URL")
	}
	return nil
}

func canonicalManifestJSON(manifest releaseManifest) ([]byte, string, error) {
	if err := validateReleaseManifest(manifest); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(raw)
	return raw, fmt.Sprintf("%x", digest[:]), nil
}
