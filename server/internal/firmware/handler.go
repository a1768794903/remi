package firmware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	Client *http.Client
	Repo   string
}

type Response struct {
	Version           string   `json:"version"`
	MinVersion        string   `json:"min_version"`
	MinAppVersion     string   `json:"min_app_version"`
	MinAppVersionCode string   `json:"min_app_version_code"`
	ZipURL            string   `json:"zip_url"`
	Draft             bool     `json:"draft"`
	OTAUpdateSteps    []string `json:"ota_update_steps"`
	LegacySecureDFU   bool     `json:"is_legacy_secure_dfu"`
	Changelog         any      `json:"changelog"`
}

type release struct {
	TagName     string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

var tagPattern = regexp.MustCompile(`(?i)^(Omi_CV1|Omi_DK2|OmiGlass|OpenGlass|Friend)_v[0-9]+(?:\.[0-9]+){1,2}$`)

func (h Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (h Handler) repo() string {
	if h.Repo != "" {
		return h.Repo
	}
	if v := os.Getenv("FIRMWARE_GITHUB_REPO"); v != "" {
		return v
	}
	return "BasedHardware/omi"
}

func devicePrefix(model string) string {
	switch model {
	case "Omi DevKit 2":
		return "Omi_DK2"
	case "OpenGlass":
		return "OpenGlass"
	case "Omi CV 1", "nrf5340":
		return "Omi_CV1"
	case "OMI Glass", "OmiGlass":
		return "OmiGlass"
	case "Friend DevKit 1", "Friend":
		return "Friend"
	default:
		return ""
	}
}

func parseVersion(s string) ([]int, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "v")
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return nil, false
	}
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out, true
}

func compare(a, b []int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func metadata(body string) map[string]any {
	result := map[string]any{}
	start, end := strings.Index(body, "<!-- KEY_VALUE_START"), strings.Index(body, "KEY_VALUE_END -->")
	if start < 0 || end <= start {
		return result
	}
	for _, line := range strings.Split(body[start+len("<!-- KEY_VALUE_START"):end], "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "ota_update_steps" {
			var xs []string
			for _, x := range strings.Split(value, ",") {
				if strings.TrimSpace(x) != "" {
					xs = append(xs, strings.TrimSpace(x))
				}
			}
			result[key] = xs
		} else if key == "changelog" {
			var xs []string
			for _, x := range strings.Split(value, "|") {
				if strings.TrimSpace(x) != "" {
					xs = append(xs, strings.TrimSpace(x))
				}
			}
			result[key] = xs
		} else {
			result[key] = value
		}
	}
	return result
}

func (h Handler) releases(r *http.Request) ([]release, error) {
	var releases []release
	for page := 1; page <= 20; page++ {
		endpoint := "https://api.github.com/repos/" + h.repo() + "/releases?per_page=100&page=" + strconv.Itoa(page)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		if err != nil { return nil, err }
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token := os.Getenv("GITHUB_TOKEN"); token != "" { req.Header.Set("Authorization", "Bearer "+token) }
		resp, err := h.client().Do(req)
		if err != nil { return nil, err }
		var pageReleases []release
		decodeErr := json.NewDecoder(resp.Body).Decode(&pageReleases)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK { return nil, fmt.Errorf("github releases returned %s", resp.Status) }
		if decodeErr != nil { return nil, decodeErr }
		releases = append(releases, pageReleases...)
		if len(pageReleases) < 100 { break }
	}
	return releases, nil
}

func validReleases(all []release, prefix string) []release {
	result := make([]release, 0)
	for _, x := range all {
		if x.Draft || x.Prerelease || x.PublishedAt == "" || !tagPattern.MatchString(x.TagName) || !strings.HasPrefix(strings.ToLower(x.TagName), strings.ToLower(prefix)+"_v") {
			continue
		}
		m := metadata(x.Body)
		if _, ok := m["release_firmware_version"]; ok {
			result = append(result, x)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].PublishedAt > result[j].PublishedAt })
	return result
}

func extract(model string, x release) (Response, error) {
	m := metadata(x.Body)
	wantBin := model == "OMI Glass" || model == "OmiGlass"
	suffix := ".zip"
	contains := "ota"
	if wantBin {
		suffix = ".bin"
		contains = ""
	}
	url := ""
	for _, a := range x.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), suffix) && (contains == "" || strings.Contains(strings.ToLower(a.Name), contains)) {
			url = a.BrowserDownloadURL
			break
		}
	}
	if url == "" {
		return Response{}, fmt.Errorf("no firmware asset found")
	}
	legacy := true
	if v, ok := m["is_legacy_secure_dfu"].(string); ok {
		legacy = strings.ToLower(v) != "false"
	}
	resp := Response{Draft: false, ZipURL: url, LegacySecureDFU: legacy}
	if v, ok := m["release_firmware_version"].(string); ok {
		resp.Version = v
	}
	if v, ok := m["minimum_firmware_required"].(string); ok {
		resp.MinVersion = v
	}
	if v, ok := m["minimum_app_version"].(string); ok {
		resp.MinAppVersion = v
	}
	if v, ok := m["minimum_app_version_code"].(string); ok {
		resp.MinAppVersionCode = v
	}
	if v, ok := m["ota_update_steps"].([]string); ok {
		resp.OTAUpdateSteps = v
	}
	if v, ok := m["changelog"]; ok {
		resp.Changelog = v
	} else {
		resp.Changelog = ""
	}
	return resp, nil
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("device_model")
	prefix := devicePrefix(model)
	if prefix == "" {
		http.Error(w, `{"detail":"Device not found"}`, 404)
		return
	}
	releases, err := h.releases(r)
	if err != nil {
		http.Error(w, `{"detail":"No releases found for the repository"}`, 404)
		return
	}
	candidates := validReleases(releases, prefix)
	path := r.URL.Path
	var selected *release
	if path == "/v2/firmware/latest" {
		current, ok := parseVersion(r.URL.Query().Get("firmware_revision"))
		if !ok {
			http.Error(w, `{"detail":"Could not determine current firmware version"}`, 400)
			return
		}
		for i := range candidates {
			m := metadata(candidates[i].Body)
			v, ok := m["release_firmware_version"].(string)
			if !ok {
				continue
			}
			got, valid := parseVersion(v)
			if !valid || compare(got, current) <= 0 {
				continue
			}
			if req, ok := m["minimum_firmware_required"].(string); ok {
				min, valid := parseVersion(req)
				if valid && compare(current, min) < 0 {
					continue
				}
			}
			selected = &candidates[i]
			break
		}
	} else if path == "/v2/firmware/version" {
		target, ok := parseVersion(r.URL.Query().Get("version"))
		if !ok {
			http.Error(w, `{"detail":"Could not parse requested firmware version"}`, 400)
			return
		}
		for i := range candidates {
			v, ok := metadata(candidates[i].Body)["release_firmware_version"].(string)
			if ok {
				got, valid := parseVersion(v)
				if valid && compare(got, target) == 0 {
					selected = &candidates[i]
					break
				}
			}
		}
	} else if len(candidates) > 0 {
		selected = &candidates[0]
	}
	if selected == nil {
		http.Error(w, `{"detail":"No suitable firmware update found for your device version."}`, 404)
		return
	}
	result, err := extract(model, *selected)
	if err != nil {
		http.Error(w, `{"detail":"`+err.Error()+`"}`, 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
