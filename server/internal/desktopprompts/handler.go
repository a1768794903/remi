package desktopprompts

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"remi/server/internal/auth"
)

type Handler struct{ DB *sql.DB }

var allowedTypes = map[string]bool{"stars": true, "nps": true, "choice": true, "banner": true}

func rolloutBucket(uid, promptID string) int {
	sum := sha256.Sum256([]byte(promptID + ":" + uid))
	value, _ := strconv.ParseUint(hex.EncodeToString(sum[:4]), 16, 32)
	return int(value % 100)
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		h.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if h.DB == nil {
		h.writeError(w, http.StatusServiceUnavailable, "desktop prompt storage is not configured")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "stable"
	}
	build, _ := strconv.Atoi(r.URL.Query().Get("build"))
	rows, err := h.DB.QueryContext(r.Context(), `SELECT external_id,prompt_type,question,options,cta_label,cta_url,trigger_kind,trigger_count,max_per_day,audience FROM desktop_prompts WHERE active=TRUE ORDER BY external_id LIMIT 50`)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "desktop prompts unavailable")
		return
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, typ, question, triggerKind string
		var optionsRaw, audienceRaw []byte
		var ctaLabel, ctaURL sql.NullString
		var triggerCount, maxPerDay int
		if err := rows.Scan(&id, &typ, &question, &optionsRaw, &ctaLabel, &ctaURL, &triggerKind, &triggerCount, &maxPerDay, &audienceRaw); err != nil {
			h.writeError(w, http.StatusInternalServerError, "desktop prompts unavailable")
			return
		}
		if !allowedTypes[typ] || strings.TrimSpace(question) == "" {
			continue
		}
		var options []any
		var audience map[string]any
		_ = json.Unmarshal(optionsRaw, &options)
		_ = json.Unmarshal(audienceRaw, &audience)
		if !matchesAudience(audience, uid, id, channel, build) {
			continue
		}
		if len(options) > 6 {
			options = options[:6]
		}
		item := map[string]any{"id": id, "type": typ, "question": question, "options": options, "trigger_kind": triggerKind, "trigger_count": triggerCount, "max_per_day": maxPerDay}
		if ctaLabel.Valid {
			item["cta_label"] = ctaLabel.String
		}
		if ctaURL.Valid {
			item["cta_url"] = ctaURL.String
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		h.writeError(w, http.StatusInternalServerError, "desktop prompts unavailable")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"prompts": out})
}

func matchesAudience(audience map[string]any, uid, id, channel string, build int) bool {
	if audience == nil {
		return rolloutBucket(uid, id) < 100
	}
	if channels, ok := audience["channels"].([]any); ok && len(channels) > 0 {
		found := false
		for _, item := range channels {
			if item == channel {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	if min, ok := audience["min_build"].(float64); ok && build > 0 && min > 0 && float64(build) < min {
		return false
	}
	pct := 100.0
	if value, ok := audience["rollout_pct"].(float64); ok {
		pct = value
	}
	return rolloutBucket(uid, id) < int(pct)
}

func (h Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func (h Handler) writeError(w http.ResponseWriter, status int, detail string) {
	h.writeJSON(w, status, map[string]string{"detail": detail})
}
