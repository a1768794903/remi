package releases

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Release struct {
	ID                int64
	Version           string
	BuildNumber       int
	DownloadURL       string
	ManualDownloadURL string
	Signature         string
	PublishedAt       string
	Changelog         []string
	Live              bool
	Critical          bool
	Channel           string
	Platform          string
	InstallerURL      string
	FeedURL           string
}

type Handler struct {
	DB     *sql.DB
	Secret string
}

type UpdatePolicy struct {
	ID                 string   `json:"id"`
	Active             bool     `json:"active"`
	Severity           string   `json:"severity"`
	MaximumBuildNumber *int     `json:"maximum_build_number"`
	LatestBuildNumber  *int     `json:"latest_build_number"`
	Title              *string  `json:"title"`
	Message            *string  `json:"message"`
	CTAText            string   `json:"cta_text"`
	DownloadURL        string   `json:"download_url"`
	CanDismiss         bool     `json:"can_dismiss"`
	Platforms          []string `json:"platforms"`
}

func defaultUpdatePolicy() UpdatePolicy {
	return UpdatePolicy{
		ID: "current", Severity: "none", CTAText: "Download latest",
		DownloadURL: "/v2/desktop/download/latest", CanDismiss: true,
	}
}

type releaseRequest struct {
	Version           string   `json:"version"`
	BuildNumber       int      `json:"build_number"`
	DownloadURL       string   `json:"download_url"`
	ManualDownloadURL string   `json:"manual_download_url"`
	Signature         string   `json:"ed_signature"`
	Changelog         []string `json:"changelog"`
	Live              bool     `json:"is_live"`
	Critical          bool     `json:"is_critical"`
	Channel           string   `json:"channel"`
	Platform          string   `json:"platform"`
	InstallerURL      string   `json:"installer_url"`
	FeedURL           string   `json:"feed_url"`
}

func AppcastXML(releases []Release, platform string) string {
	sort.SliceStable(releases, func(i, j int) bool { return releases[i].BuildNumber > releases[j].BuildNumber })
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<rss version=\"2.0\" xmlns:sparkle=\"http://www.andymatuschak.org/xml-namespaces/sparkle\"><channel><title>Omi Desktop Updates</title>")
	seen := map[string]bool{}
	for _, release := range releases {
		channel := release.Channel
		if channel == "" {
			channel = "staging"
		}
		if !release.Live || seen[channel] {
			continue
		}
		seen[channel] = true
		changes := "<p>Bug fixes and improvements.</p>"
		if len(release.Changelog) > 0 {
			var items strings.Builder
			for _, item := range release.Changelog {
				items.WriteString("<li>" + html.EscapeString(item) + "</li>")
			}
			changes = "<ul>" + items.String() + "</ul>"
		}
		b.WriteString("<item><title>Omi " + html.EscapeString(release.Version) + "</title><sparkle:version>" + itoa(release.BuildNumber) + "</sparkle:version><sparkle:shortVersionString>" + html.EscapeString(release.Version) + "</sparkle:shortVersionString><description><![CDATA[" + strings.ReplaceAll(changes, "]]>", "]] ]]>") + "]]></description><pubDate>" + html.EscapeString(release.PublishedAt) + "</pubDate><enclosure url=\"" + html.EscapeString(release.DownloadURL) + "\" type=\"application/octet-stream\" sparkle:os=\"" + html.EscapeString(platform) + "\" sparkle:edSignature=\"" + html.EscapeString(release.Signature) + "\" />")
		if release.Channel != "stable" {
			b.WriteString("<sparkle:channel>" + html.EscapeString(channel) + "</sparkle:channel>")
		}
		if release.Critical {
			b.WriteString("<sparkle:criticalUpdate />")
		}
		b.WriteString("</item>")
	}
	b.WriteString("</channel></rss>\n")
	return b.String()
}

func manualDownloadURL(release Release) string {
	if strings.TrimSpace(release.ManualDownloadURL) != "" {
		return strings.TrimSpace(release.ManualDownloadURL)
	}
	if strings.HasSuffix(release.DownloadURL, "/Omi.zip") {
		return strings.TrimSuffix(release.DownloadURL, "Omi.zip") + "Omi.dmg"
	}
	return release.DownloadURL
}

func itoa(value int) string { return strconv.Itoa(value) }

func (h Handler) Appcast(w http.ResponseWriter, r *http.Request) {
	releases, err := h.list(r.Context())
	if err != nil {
		http.Error(w, "Desktop update feed is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Cache-Control", "max-age=300")
	platform := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("platform")))
	if platform == "" {
		platform = "macos"
	}
	filtered := make([]Release, 0, len(releases))
	for _, release := range releases {
		if release.Platform == "" || release.Platform == platform {
			filtered = append(filtered, release)
		}
	}
	_, _ = w.Write([]byte(AppcastXML(filtered, platform)))
}
func (h Handler) Latest(w http.ResponseWriter, r *http.Request) {
	releases, err := h.list(r.Context())
	if err != nil {
		writeJSONError(w, 500, "Failed to fetch releases")
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("channel")))
	if channel == "" {
		channel = "stable"
	}
	if release, ok := latestReleaseForChannel(releases, channel); ok {
		writeJSON(w, 200, map[string]any{"version": release.Version, "build_number": release.BuildNumber, "download_url": release.DownloadURL, "is_critical": release.Critical, "channel": channel})
		return
	}
	writeJSONError(w, 404, "No live releases found")
}
func (h Handler) Download(w http.ResponseWriter, r *http.Request) {
	releases, err := h.list(r.Context())
	if err != nil {
		writeJSONError(w, 500, "Failed to fetch releases")
		return
	}
	channel := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("channel")))
	if channel == "" {
		channel = "stable"
	}
	if release, ok := latestReleaseForChannel(releases, channel); ok {
		http.Redirect(w, r, manualDownloadURL(release), http.StatusTemporaryRedirect)
		return
	}
	writeJSONError(w, 404, "No live releases found")
}

func (h Handler) BetaDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Set("channel", "beta")
	r.URL.RawQuery = q.Encode()
	h.Download(w, r)
}

func latestPlatformRelease(releases []Release, platform, channel string, fallback bool) (Release, string, bool) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = "stable"
	}
	if release, ok := latestReleaseForPlatformChannel(releases, platform, channel); ok {
		return release, channel, true
	}
	if fallback {
		other := "stable"
		if channel == "stable" {
			other = "beta"
		}
		if release, ok := latestReleaseForPlatformChannel(releases, platform, other); ok {
			return release, other, true
		}
	}
	return Release{}, channel, false
}

func latestReleaseForPlatformChannel(releases []Release, platform, channel string) (Release, bool) {
	var latest Release
	found := false
	for _, release := range releases {
		if !release.Live || release.Platform != platform || strings.ToLower(release.Channel) != channel {
			continue
		}
		if !found || release.BuildNumber > latest.BuildNumber {
			latest, found = release, true
		}
	}
	return latest, found
}

func (h Handler) WindowsUpdateFeed(w http.ResponseWriter, r *http.Request) {
	requested := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("channel")))
	if requested == "" {
		requested = "stable"
	}
	if requested != "stable" && requested != "beta" {
		writeJSONError(w, http.StatusBadRequest, "invalid channel")
		return
	}
	releases, err := h.list(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "desktop update feed is unavailable")
		return
	}
	release, served, ok := latestPlatformRelease(releases, "windows", requested, requested == "beta")
	if !ok || strings.TrimSpace(release.FeedURL) == "" {
		writeJSONError(w, http.StatusNotFound, "No Windows update feed found for channel: "+requested)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requested_channel": requested, "served_channel": served, "version": release.Version, "feed_url": release.FeedURL})
}

func (h Handler) WindowsDownload(w http.ResponseWriter, r *http.Request) {
	requested := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("channel")))
	if requested == "" {
		requested = "stable"
	}
	releases, err := h.list(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "desktop releases are unavailable")
		return
	}
	release, served, ok := latestPlatformRelease(releases, "windows", requested, true)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "No Windows release found")
		return
	}
	url := release.InstallerURL
	if url == "" {
		url = manualDownloadURL(release)
	}
	if url == "" {
		writeJSONError(w, http.StatusNotFound, "No Windows installer found")
		return
	}
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
	_ = served
}

func (h Handler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	// Go currently has no remote Firestore policy store. The safe parity default
	// is the Python service's inactive policy: clients continue using appcast
	// and the normal download route, while no server-controlled banner is shown.
	writeJSON(w, http.StatusOK, defaultUpdatePolicy())
}

func (h Handler) ClearCache(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(os.Getenv("ADMIN_KEY")) == "" || r.Header.Get("secret-key") != os.Getenv("ADMIN_KEY") {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Desktop releases cache cleared successfully"})
}

func (h Handler) Manifest(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(os.Getenv("ADMIN_KEY")) == "" || r.Header.Get("secret-key") != os.Getenv("ADMIN_KEY") {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	id := r.PathValue("release_id")
	var release Release
	var raw []byte
	err := h.DB.QueryRowContext(r.Context(), "SELECT id,version,build_number,download_url,COALESCE(manual_download_url,''),ed_signature,published_at,changelog,is_live,is_critical,channel,platform,COALESCE(installer_url,''),COALESCE(feed_url,'') FROM desktop_releases WHERE CAST(id AS CHAR)=? OR version=?", id, id).Scan(&release.ID, &release.Version, &release.BuildNumber, &release.DownloadURL, &release.ManualDownloadURL, &release.Signature, &release.PublishedAt, &raw, &release.Live, &release.Critical, &release.Channel, &release.Platform, &release.InstallerURL, &release.FeedURL)
	if err == sql.ErrNoRows {
		writeJSONError(w, http.StatusNotFound, "desktop release manifest not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to read desktop release manifest")
		return
	}
	_ = json.Unmarshal(raw, &release.Changelog)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "manifest": release})
}

func latestReleaseForChannel(releases []Release, channel string) (Release, bool) {
	channel = strings.ToLower(strings.TrimSpace(channel))
	var latest Release
	found := false
	for _, release := range releases {
		if !release.Live || strings.ToLower(strings.TrimSpace(release.Channel)) != channel {
			continue
		}
		if !found || release.BuildNumber > latest.BuildNumber {
			latest = release
			found = true
		}
	}
	return latest, found
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid or missing X-Release-Secret header")
		return
	}
	var input releaseRequest
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.Version) == "" || input.BuildNumber < 0 || strings.TrimSpace(input.DownloadURL) == "" || strings.TrimSpace(input.Signature) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid release")
		return
	}
	if input.Channel == "" {
		input.Channel = "staging"
	}
	if input.Platform == "" {
		input.Platform = "macos"
	}
	if input.Platform != "macos" && input.Platform != "windows" && input.Platform != "linux" {
		writeJSONError(w, http.StatusBadRequest, "invalid platform")
		return
	}
	raw, _ := json.Marshal(input.Changelog)
	result, err := h.DB.ExecContext(r.Context(), "INSERT INTO desktop_releases (version, build_number, download_url, manual_download_url, ed_signature, published_at, changelog, is_live, is_critical, channel, platform, installer_url, feed_url, created_at, updated_at) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), UTC_TIMESTAMP(6), UTC_TIMESTAMP(6))", input.Version, input.BuildNumber, input.DownloadURL, input.ManualDownloadURL, input.Signature, time.Now().UTC().Format(time.RFC3339), raw, input.Live, input.Critical, input.Channel, input.Platform, input.InstallerURL, input.FeedURL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to create release")
		return
	}
	id, _ := result.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "doc_id": strconv.FormatInt(id, 10), "message": "Release v" + input.Version + " created successfully"})
}

func (h Handler) Promote(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "Invalid or missing X-Release-Secret header")
		return
	}
	var input struct {
		DocID string `json:"doc_id"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || strings.TrimSpace(input.DocID) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid doc_id")
		return
	}
	var old string
	if err := h.DB.QueryRowContext(r.Context(), "SELECT channel FROM desktop_releases WHERE id = ?", input.DocID).Scan(&old); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to promote release")
		return
	}
	next := map[string]string{"staging": "beta", "beta": "stable"}[old]
	if next == "" {
		writeJSONError(w, http.StatusBadRequest, "Failed to promote: release is already stable")
		return
	}
	if _, err := h.DB.ExecContext(r.Context(), "UPDATE desktop_releases SET channel = ?, updated_at = UTC_TIMESTAMP(6) WHERE id = ?", next, input.DocID); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to promote release")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "doc_id": input.DocID, "old_channel": old, "new_channel": next, "message": "Release promoted from " + old + " to " + next})
}

func (h Handler) authorized(r *http.Request) bool {
	want, got := []byte(strings.TrimSpace(h.Secret)), []byte(strings.TrimSpace(r.Header.Get("X-Release-Secret")))
	return len(want) > 0 && len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func (h Handler) list(ctx context.Context) ([]Release, error) {
	if h.DB == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := h.DB.QueryContext(ctx, "SELECT id, version, build_number, download_url, COALESCE(manual_download_url,''), ed_signature, published_at, changelog, is_live, is_critical, channel, platform, COALESCE(installer_url,''), COALESCE(feed_url,'') FROM desktop_releases ORDER BY build_number DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var x Release
		var raw []byte
		if err := rows.Scan(&x.ID, &x.Version, &x.BuildNumber, &x.DownloadURL, &x.ManualDownloadURL, &x.Signature, &x.PublishedAt, &raw, &x.Live, &x.Critical, &x.Channel, &x.Platform, &x.InstallerURL, &x.FeedURL); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &x.Changelog)
		out = append(out, x)
	}
	return out, rows.Err()
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
