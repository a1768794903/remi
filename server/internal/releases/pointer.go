package releases

import (
	"errors"
	"fmt"
)

type channelPointer struct {
	Platform   string `json:"platform"`
	Channel    string `json:"channel"`
	ReleaseID  string `json:"release_id"`
	Version    string `json:"version"`
	Build      int    `json:"build_number"`
	Generation int64  `json:"generation"`
}

func buildBetaPointer(current channelPointer, manifest releaseManifest, expectedGeneration *int64) (channelPointer, error) {
	if err := validateReleaseManifest(manifest); err != nil {
		return current, err
	}
	if manifest.Platform != "macos" || manifest.Channel != "beta" {
		return current, errors.New("beta pointer requires a macos beta manifest")
	}
	if current.ReleaseID == manifest.ReleaseID {
		return current, nil
	}
	if expectedGeneration != nil && *expectedGeneration != current.Generation {
		return current, fmt.Errorf("generation mismatch: expected %d, current %d", *expectedGeneration, current.Generation)
	}
	if current.Build > 0 && manifest.BuildNumber <= current.Build {
		return current, errors.New("channel pointers are roll-forward only")
	}
	return channelPointer{Platform: "macos", Channel: "beta", ReleaseID: manifest.ReleaseID, Version: manifest.Version, Build: manifest.BuildNumber, Generation: current.Generation + 1}, nil
}

func buildBreakglassPointer(current channelPointer, manifest releaseManifest, expectedGeneration int64, expectedCurrent string) (channelPointer, error) {
	if err := validateReleaseManifest(manifest); err != nil {
		return current, err
	}
	if manifest.Platform != "macos" || manifest.Channel != "beta" {
		return current, errors.New("breakglass requires a macos beta manifest")
	}
	if current.Generation != expectedGeneration || current.ReleaseID != expectedCurrent {
		return current, errors.New("breakglass pointer precondition failed")
	}
	return channelPointer{Platform: "macos", Channel: "beta", ReleaseID: manifest.ReleaseID, Version: manifest.Version, Build: manifest.BuildNumber, Generation: current.Generation + 1}, nil
}
