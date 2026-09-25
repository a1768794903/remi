package releases

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

func (h Handler) betaTokenAuthorized(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv("BETA_PROMOTION_TOKEN"))
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	return want != "" && got == "Bearer "+want
}

func (h Handler) adminAuthorized(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv("ADMIN_KEY"))
	return want != "" && strings.TrimSpace(r.Header.Get("secret-key")) == want
}

func readBetaControl(tx *sql.Tx) (betaControl, error) {
	var control betaControl
	err := tx.QueryRow(`SELECT promotion_enabled,COALESCE(reserved_tag,''),COALESCE(reserved_build_number,0),generation FROM desktop_beta_control WHERE id=1 FOR UPDATE`).Scan(&control.PromotionEnabled, &control.ReservedTag, &control.ReservedBuild, &control.Generation)
	if err == sql.ErrNoRows {
		return control, nil
	}
	return control, err
}

func writeBetaControl(tx *sql.Tx, control betaControl) error {
	_, err := tx.Exec(`INSERT INTO desktop_beta_control(id,promotion_enabled,reserved_tag,reserved_build_number,generation,updated_at) VALUES(1,?,?,?,?,?) ON DUPLICATE KEY UPDATE promotion_enabled=VALUES(promotion_enabled),reserved_tag=VALUES(reserved_tag),reserved_build_number=VALUES(reserved_build_number),generation=VALUES(generation),updated_at=VALUES(updated_at)`, control.PromotionEnabled, nullString(control.ReservedTag), nullableBuild(control.ReservedTag, control.ReservedBuild), control.Generation, time.Now().UTC())
	return err
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableBuild(tag string, build int) any {
	if strings.TrimSpace(tag) == "" {
		return nil
	}
	return build
}

func (h Handler) ReserveBetaCandidate(w http.ResponseWriter, r *http.Request) {
	if !h.betaTokenAuthorized(r) {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var input struct {
		Tag string `json:"tag"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid candidate")
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	control, err := readBetaControl(tx)
	if err == nil {
		control, err = reserveBetaCandidate(control, strings.TrimSpace(input.Tag))
	}
	if err == nil {
		err = writeBetaControl(tx, control)
	}
	if err != nil {
		_ = tx.Rollback()
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "failed to commit beta reservation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": control.ReservedTag, "generation": control.Generation})
}

func (h Handler) SetBetaAdmission(w http.ResponseWriter, r *http.Request) {
	if !h.adminAuthorized(r) {
		writeJSONError(w, http.StatusForbidden, "You are not authorized to perform this action")
		return
	}
	if h.DB == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "release storage is not configured")
		return
	}
	var input struct {
		PromotionEnabled *bool `json:"promotion_enabled"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.PromotionEnabled == nil {
		writeJSONError(w, http.StatusBadRequest, "promotion_enabled is required")
		return
	}
	var control betaControl
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err == nil {
		var readErr error
		control, readErr = readBetaControl(tx)
		err = readErr
		if err == nil {
			control, err = setBetaAdmission(control, *input.PromotionEnabled)
		}
		if err == nil {
			err = writeBetaControl(tx, control)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"promotion_enabled": control.PromotionEnabled, "generation": control.Generation})
}
