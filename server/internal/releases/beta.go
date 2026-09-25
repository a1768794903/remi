package releases

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var betaCandidateTagRE = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)\+([1-9][0-9]*)-macos$`)

type betaCandidateTag struct {
	Tag                 string
	Major, Minor, Patch int
	Build               int
}

type betaControl struct {
	PromotionEnabled bool
	ReservedTag      string
	ReservedBuild    int
	Generation       int64
}

func parseBetaCandidateTag(tag string) (betaCandidateTag, error) {
	matches := betaCandidateTagRE.FindStringSubmatch(tag)
	if matches == nil {
		return betaCandidateTag{}, errors.New("invalid macOS beta candidate tag")
	}
	values := make([]int, 4)
	for i := range values {
		value, err := strconv.Atoi(matches[i+1])
		if err != nil {
			return betaCandidateTag{}, errors.New("invalid macOS beta candidate tag")
		}
		values[i] = value
	}
	return betaCandidateTag{Tag: tag, Major: values[0], Minor: values[1], Patch: values[2], Build: values[3]}, nil
}

func compareBetaVersion(a, b betaCandidateTag) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}, {a.Build, b.Build}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func reserveBetaCandidate(current betaControl, tag string) (betaControl, error) {
	candidate, err := parseBetaCandidateTag(tag)
	if err != nil {
		return current, err
	}
	if current.ReservedTag != "" {
		reserved, parseErr := parseBetaCandidateTag(current.ReservedTag)
		if parseErr != nil || compareBetaVersion(candidate, reserved) < 0 {
			return current, errors.New("candidate reservation must roll forward")
		}
		if compareBetaVersion(candidate, reserved) == 0 {
			return current, nil
		}
	}
	current.ReservedTag = candidate.Tag
	current.ReservedBuild = candidate.Build
	current.Generation++
	return current, nil
}

func setBetaAdmission(current betaControl, enabled bool) (betaControl, error) {
	if current.Generation == 0 && !enabled {
		current.Generation = 1
		return current, nil
	}
	if current.PromotionEnabled == enabled {
		return current, nil
	}
	if enabled && current.ReservedTag == "" {
		return current, errors.New("beta admission cannot resume without a reservation")
	}
	current.PromotionEnabled = enabled
	current.Generation++
	return current, nil
}

func captureBetaAdmission(current betaControl, tag string) (betaControl, error) {
	candidate, err := parseBetaCandidateTag(tag)
	if err != nil {
		return current, err
	}
	if !current.PromotionEnabled {
		return current, errors.New("beta admission is disabled")
	}
	if current.ReservedTag != candidate.Tag || current.ReservedBuild != candidate.Build {
		return current, fmt.Errorf("beta admission reservation does not match candidate")
	}
	return current, nil
}
