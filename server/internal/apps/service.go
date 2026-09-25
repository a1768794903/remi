package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
	"remi/server/internal/chatfiles"
)

var ErrAppNotFound = errors.New("app not found")
var ErrInvalidOwnerMigration = errors.New("invalid app owner migration")

type App struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	UID                 *string        `json:"uid,omitempty"`
	Private             bool           `json:"private"`
	Approved            bool           `json:"approved"`
	Status              string         `json:"status"`
	Category            string         `json:"category"`
	Author              string         `json:"author"`
	Description         string         `json:"description"`
	Image               string         `json:"image"`
	Capabilities        []string       `json:"capabilities"`
	ExternalIntegration map[string]any `json:"external_integration,omitempty"`
	ChatTools           []any          `json:"chat_tools,omitempty"`
	Installs            int            `json:"installs"`
	Popular             bool           `json:"popular"`
	RatingAvg           *float64       `json:"rating_avg,omitempty"`
	RatingCount         int            `json:"rating_count"`
	Enabled             bool           `json:"enabled"`
	IsPaid              bool           `json:"is_paid"`
	Price               float64        `json:"price"`
	Disabled            bool           `json:"disabled"`
	DisabledReason      string         `json:"disabled_reason,omitempty"`
}

type Service struct{ DB *sql.DB }

// MigrateOwner moves private app ownership and relational memories in one
// transaction. The caller must authenticate the source identity separately;
// this method only performs the durable, UID-scoped mutation.
func (s Service) MigrateOwner(ctx context.Context, newUID, oldUID string) error {
	newUID, oldUID = strings.TrimSpace(newUID), strings.TrimSpace(oldUID)
	if s.DB == nil || newUID == "" || oldUID == "" || newUID == oldUID {
		return ErrInvalidOwnerMigration
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var oldID, newID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE external_uid=? FOR UPDATE`, oldUID).Scan(&oldID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidOwnerMigration
		}
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE external_uid=? FOR UPDATE`, newUID).Scan(&newID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidOwnerMigration
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugins_data SET uid=?,updated_at=UTC_TIMESTAMP(6) WHERE uid=?`, newUID, oldUID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE memories SET user_id=? WHERE user_id=?`, newID, oldID); err != nil {
		return err
	}
	return tx.Commit()
}

var appColumns = "id,name,uid,private,approved,status,category,author,description,image,capabilities,external_integration,chat_tools,installs,popular,rating_avg,rating_count,is_paid,price,disabled,disabled_reason"

type APIKey struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret,omitempty"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

func (s Service) CreateAPIKey(ctx context.Context, uid, appID string) (APIKey, error) {
	app, err := s.Get(ctx, uid, appID)
	if err != nil {
		return APIKey{}, err
	}
	if app.UID == nil || *app.UID != uid {
		return APIKey{}, errors.New("you are not authorized to create API keys for this app")
	}
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		return APIKey{}, err
	}
	secret := "sk_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(secret))
	id := newID()
	label := "App API key"
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_api_keys (id,app_id,key_hash,label,created_at) VALUES (?,?,?,?,?)`, id, appID, hex.EncodeToString(sum[:]), label, now)
	return APIKey{ID: id, Secret: secret, Label: label, CreatedAt: now}, err
}
func (s Service) ListAPIKeys(ctx context.Context, uid, appID string) ([]APIKey, error) {
	app, err := s.Get(ctx, uid, appID)
	if err != nil {
		return nil, err
	}
	if app.UID == nil || *app.UID != uid {
		return nil, errors.New("forbidden")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,label,created_at FROM app_api_keys WHERE app_id=? ORDER BY created_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Label, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (s Service) DeleteAPIKey(ctx context.Context, uid, appID, keyID string) error {
	app, err := s.Get(ctx, uid, appID)
	if err != nil {
		return err
	}
	if app.UID == nil || *app.UID != uid {
		return errors.New("forbidden")
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM app_api_keys WHERE app_id=? AND id=?`, appID, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (s Service) SetTester(ctx context.Context, uid, appID string, access bool) error {
	if access {
		_, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO app_testers(uid,app_id,created_at) VALUES (?,?,NOW(6))`, uid, appID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM app_testers WHERE uid=? AND app_id=?`, uid, appID)
	return err
}
func (s Service) IsTester(ctx context.Context, uid string) bool {
	var n int
	return s.DB.QueryRowContext(ctx, `SELECT 1 FROM app_testers WHERE uid=? LIMIT 1`, uid).Scan(&n) == nil
}
func (s Service) SummaryIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT app_id FROM summary_app_ids ORDER BY app_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s Service) SetSummaryID(ctx context.Context, id string, add bool) error {
	if add {
		_, err := s.DB.ExecContext(ctx, `INSERT IGNORE INTO summary_app_ids(app_id,created_at) VALUES (?,NOW(6))`, id)
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM summary_app_ids WHERE app_id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type MutationInput struct {
	ID                  string         `json:"id"`
	Name                *string        `json:"name"`
	Category            *string        `json:"category"`
	Author              *string        `json:"author"`
	Description         *string        `json:"description"`
	Image               *string        `json:"image"`
	Capabilities        *[]string      `json:"capabilities"`
	ExternalIntegration map[string]any `json:"external_integration"`
	Private             *bool          `json:"private"`
	IsPaid              *bool          `json:"is_paid"`
	Price               *float64       `json:"price"`
	Disabled            *bool          `json:"disabled"`
	DisabledReason      *string        `json:"disabled_reason"`
}

type Review struct {
	UID         string  `json:"uid"`
	RatedAt     string  `json:"rated_at"`
	Score       float64 `json:"score"`
	Review      string  `json:"review"`
	Username    string  `json:"username,omitempty"`
	Response    string  `json:"response,omitempty"`
	RespondedAt *string `json:"responded_at,omitempty"`
}

func (s Service) List(ctx context.Context, uid string, includePrivate bool) ([]App, error) {
	if s.DB == nil {
		return nil, errors.New("app database is not configured")
	}
	query := `SELECT ` + appColumns + ` FROM plugins_data WHERE approved = 1 AND private = 0`
	args := []any{}
	if includePrivate && uid != "" {
		query += ` OR uid = ?`
		args = append(args, uid)
	}
	query += ` ORDER BY installs DESC, name ASC`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []App{}
	for rows.Next() {
		item, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		item.Enabled = s.enabled(ctx, uid, item.ID)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Service) Get(ctx context.Context, uid, id string) (App, error) {
	if s.DB == nil {
		return App{}, errors.New("app database is not configured")
	}
	row := s.DB.QueryRowContext(ctx, `SELECT `+appColumns+` FROM plugins_data WHERE id = ? AND (private = 0 AND approved = 1 OR uid = ?)`, id, uid)
	item, err := scanApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrAppNotFound
	}
	if err != nil {
		return App{}, err
	}
	item.Enabled = s.enabled(ctx, uid, item.ID)
	return item, nil
}

func (s Service) Enabled(ctx context.Context, uid string) ([]string, error) {
	if s.DB == nil {
		return nil, errors.New("app database is not configured")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT app_id FROM user_enabled_apps WHERE user_external_uid = ? ORDER BY app_id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func (s Service) Enable(ctx context.Context, uid, id string) error {
	app, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if app.Disabled {
		return errors.New("app is disabled")
	}
	if app.IsPaid {
		return errors.New("paid app authorization is not migrated")
	}
	_, err = s.DB.ExecContext(ctx, `INSERT IGNORE INTO user_enabled_apps (user_external_uid,app_id,created_at) VALUES (?,?,NOW(6))`, uid, id)
	return err
}

func (s Service) Disable(ctx context.Context, uid, id string) error {
	if s.DB == nil {
		return errors.New("app database is not configured")
	}
	result, err := s.DB.ExecContext(ctx, `DELETE FROM user_enabled_apps WHERE user_external_uid = ? AND app_id = ?`, uid, id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrAppNotFound
	}
	return nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)
}

func (s Service) Create(ctx context.Context, uid string, in MutationInput) (App, error) {
	if s.DB == nil {
		return App{}, errors.New("app database is not configured")
	}
	name := strings.TrimSpace(valueString(in.Name))
	if name == "" {
		return App{}, errors.New("name is required")
	}
	if in.IsPaid != nil && *in.IsPaid && (in.Price == nil || *in.Price < 0) {
		return App{}, errors.New("valid price is required for paid app")
	}
	id := newID()
	caps, _ := json.Marshal(valueStrings(in.Capabilities))
	ext, _ := json.Marshal(in.ExternalIntegration)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO plugins_data (id,name,uid,private,approved,status,category,author,description,image,capabilities,external_integration,created_at,updated_at,is_paid,price) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NOW(6),NOW(6),?,?)`, id, name, uid, false, false, "under-review", valueStringDefault(in.Category, "other"), valueString(in.Author), valueString(in.Description), valueString(in.Image), caps, nullJSON(ext), boolValue(in.IsPaid), floatValue(in.Price))
	if err != nil {
		return App{}, err
	}
	return s.Get(ctx, uid, id)
}

func (s Service) Update(ctx context.Context, uid, id string, in MutationInput) error {
	app, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if app.UID == nil || *app.UID != uid {
		return errors.New("you are not authorized to update this app")
	}
	sets := []string{}
	args := []any{}
	add := func(column string, value any) { sets = append(sets, column+"=?"); args = append(args, value) }
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return errors.New("name is required")
		}
		add("name", strings.TrimSpace(*in.Name))
	}
	if in.Category != nil {
		add("category", *in.Category)
	}
	if in.Author != nil {
		add("author", *in.Author)
	}
	if in.Description != nil {
		add("description", *in.Description)
	}
	if in.Image != nil {
		add("image", *in.Image)
	}
	if in.Private != nil {
		add("private", *in.Private)
	}
	if in.IsPaid != nil {
		add("is_paid", *in.IsPaid)
	}
	if in.Price != nil {
		if *in.Price < 0 {
			return errors.New("price cannot be negative")
		}
		add("price", *in.Price)
	}
	if in.Disabled != nil {
		add("disabled", *in.Disabled)
	}
	if in.DisabledReason != nil {
		add("disabled_reason", *in.DisabledReason)
	}
	if in.Capabilities != nil {
		b, _ := json.Marshal(*in.Capabilities)
		add("capabilities", b)
	}
	if in.ExternalIntegration != nil {
		b, _ := json.Marshal(in.ExternalIntegration)
		add("external_integration", b)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at=NOW(6)")
	args = append(args, id, uid)
	_, err = s.DB.ExecContext(ctx, "UPDATE plugins_data SET "+strings.Join(sets, ",")+" WHERE id=? AND uid=?", args...)
	return err
}

func (s Service) Delete(ctx context.Context, uid, id string) error {
	app, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if app.UID == nil || *app.UID != uid {
		return errors.New("you are not authorized to delete this app")
	}
	_, err = s.DB.ExecContext(ctx, "DELETE FROM plugins_data WHERE id=? AND uid=?", id, uid)
	return err
}
func (s Service) SetVisibility(ctx context.Context, uid, id string, private bool) error {
	app, err := s.Get(ctx, uid, id)
	if err != nil {
		return err
	}
	if app.UID == nil || *app.UID != uid {
		return errors.New("you are not authorized to change visibility")
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE plugins_data SET private=?,updated_at=NOW(6) WHERE id=? AND uid=?", private, id, uid)
	return err
}
func (s Service) SetApproved(ctx context.Context, id string, approved bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE plugins_data SET approved=?,status=?,updated_at=NOW(6) WHERE id=?", approved, map[bool]string{true: "approved", false: "rejected"}[approved], id)
	return err
}
func (s Service) SetPopular(ctx context.Context, id string, popular bool) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE plugins_data SET popular=?,updated_at=NOW(6) WHERE id=?", popular, id)
	return err
}
func (s Service) Public(ctx context.Context, popular bool) ([]App, error) {
	q := "SELECT " + appColumns + " FROM plugins_data WHERE approved=1 AND private=0"
	if popular {
		q += " AND popular=1"
	}
	q += " ORDER BY installs DESC,name ASC"
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, e := scanApp(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s Service) Persona(ctx context.Context, uid string) (App, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT `+appColumns+` FROM plugins_data WHERE uid=? AND JSON_CONTAINS(capabilities, JSON_QUOTE('persona')) ORDER BY created_at DESC LIMIT 1`, uid)
	item, err := scanApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrAppNotFound
	}
	return item, err
}

func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 5 {
		return 5
	}
	return score
}

func (s Service) Reviews(ctx context.Context, appID string) ([]Review, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT reviewer_uid,rated_at,score,review,username,response,responded_at FROM app_reviews WHERE app_id = ? AND review <> '' ORDER BY rated_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Review{}
	for rows.Next() {
		var item Review
		var username, response, responded sql.NullString
		if err := rows.Scan(&item.UID, &item.RatedAt, &item.Score, &item.Review, &username, &response, &responded); err != nil {
			return nil, err
		}
		item.Score = clampScore(item.Score)
		if username.Valid {
			item.Username = username.String
		}
		if response.Valid {
			item.Response = response.String
		}
		if responded.Valid {
			item.RespondedAt = &responded.String
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Service) SaveReview(ctx context.Context, uid, appID string, score float64, review, username string, update bool) error {
	app, err := s.Get(ctx, uid, appID)
	if err != nil {
		return err
	}
	if app.UID != nil && *app.UID == uid {
		return errors.New("you are not authorized to review your own app")
	}
	if app.Private && (app.UID == nil || *app.UID != uid) {
		return errors.New("you are not authorized to review this app")
	}
	if update {
		result, e := s.DB.ExecContext(ctx, `UPDATE app_reviews SET score=?,review=?,username=?,updated_at=NOW(6) WHERE app_id=? AND reviewer_uid=?`, clampScore(score), review, username, appID, uid)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return errors.New("review not found")
		}
		return nil
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO app_reviews (app_id,reviewer_uid,score,review,username,rated_at,updated_at) VALUES (?,?,?,?,?,NOW(6),NOW(6)) ON DUPLICATE KEY UPDATE score=VALUES(score),review=VALUES(review),username=VALUES(username),updated_at=NOW(6)`, appID, uid, clampScore(score), review, username)
	return err
}

func (s Service) ReplyReview(ctx context.Context, uid, appID, reviewerUID, response string) error {
	app, err := s.Get(ctx, uid, appID)
	if err != nil {
		return err
	}
	if app.UID == nil || *app.UID != uid {
		return errors.New("you are not authorized to reply to this app review")
	}
	if strings.TrimSpace(response) == "" {
		return errors.New("response is required")
	}
	result, err := s.DB.ExecContext(ctx, `UPDATE app_reviews SET response=?,responded_at=NOW(6),updated_at=NOW(6) WHERE app_id=? AND reviewer_uid=?`, response, appID, reviewerUID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return errors.New("review not found")
	}
	return nil
}

func (s Service) enabled(ctx context.Context, uid, id string) bool {
	if uid == "" || s.DB == nil {
		return false
	}
	var one int
	return s.DB.QueryRowContext(ctx, `SELECT 1 FROM user_enabled_apps WHERE user_external_uid = ? AND app_id = ?`, uid, id).Scan(&one) == nil
}

type scanner interface{ Scan(...any) error }

func scanApp(row scanner) (App, error) {
	var item App
	var uid, capabilities, integration, chatTools sql.NullString
	var avg sql.NullFloat64
	var private, approved, paid, disabled, popular int
	var price sql.NullFloat64
	err := row.Scan(&item.ID, &item.Name, &uid, &private, &approved, &item.Status, &item.Category, &item.Author, &item.Description, &item.Image, &capabilities, &integration, &chatTools, &item.Installs, &popular, &avg, &item.RatingCount, &paid, &price, &disabled, &item.DisabledReason)
	if err != nil {
		return App{}, err
	}
	item.Private, item.Approved, item.Popular, item.IsPaid, item.Disabled = private != 0, approved != 0, popular != 0, paid != 0, disabled != 0
	if uid.Valid && uid.String != "" {
		item.UID = &uid.String
	}
	if avg.Valid {
		item.RatingAvg = &avg.Float64
	}
	if price.Valid {
		item.Price = price.Float64
	}
	if capabilities.Valid && capabilities.String != "" {
		_ = json.Unmarshal([]byte(capabilities.String), &item.Capabilities)
	}
	if item.Capabilities == nil {
		item.Capabilities = []string{}
	}
	if integration.Valid && integration.String != "" {
		_ = json.Unmarshal([]byte(integration.String), &item.ExternalIntegration)
	}
	if chatTools.Valid && chatTools.String != "" {
		_ = json.Unmarshal([]byte(chatTools.String), &item.ChatTools)
	}
	return item, nil
}

type Handler struct {
	Service        Service
	Provider       chat.Provider
	ThumbnailStore chatfiles.ThumbnailStore
	Verifier       *auth.FirebaseVerifier
}

func valueString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func valueStringDefault(v *string, fallback string) string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return fallback
	}
	return *v
}
func valueStrings(v *[]string) []string {
	if v == nil {
		return []string{}
	}
	return *v
}
func boolValue(v *bool) bool { return v != nil && *v }
func floatValue(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
func nullJSON(b []byte) any {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	return b
}

func decodeMutation(r *http.Request) (MutationInput, error) {
	var in MutationInput
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/") {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			return in, err
		}
		raw := r.FormValue("app_data")
		if raw == "" {
			raw = r.FormValue("persona_data")
		}
		if raw == "" {
			return in, errors.New("app_data is required")
		}
		return in, json.Unmarshal([]byte(raw), &in)
	}
	return in, json.NewDecoder(r.Body).Decode(&in)
}

func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	in, err := decodeMutation(r)
	if err != nil {
		http.Error(w, "invalid app_data", 422)
		return
	}
	app, err := h.Service.Create(r.Context(), uid, in)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	_ = writeJSON(w, map[string]any{"status": "ok", "app_id": app.ID})
}

func (h Handler) MigrateOwner(w http.ResponseWriter, r *http.Request) {
	destination, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var in struct {
		OldID       string `json:"old_id"`
		SourceToken string `json:"source_token"`
	}
	in.OldID, in.SourceToken = strings.TrimSpace(r.URL.Query().Get("old_id")), strings.TrimSpace(r.URL.Query().Get("source_token"))
	if r.Body != nil {
		var body struct {
			OldID       string `json:"old_id"`
			SourceToken string `json:"source_token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&body); err == nil {
			if in.OldID == "" {
				in.OldID = strings.TrimSpace(body.OldID)
			}
			if in.SourceToken == "" {
				in.SourceToken = strings.TrimSpace(body.SourceToken)
			}
		}
	}
	if in.OldID == "" || in.SourceToken == "" || in.OldID == destination {
		http.Error(w, "source identity is not eligible for migration", http.StatusForbidden)
		return
	}
	if h.Verifier == nil {
		http.Error(w, "identity verification is not configured", http.StatusServiceUnavailable)
		return
	}
	source, err := h.Verifier.Verify(r.Context(), in.SourceToken)
	if err != nil || source != in.OldID {
		http.Error(w, "source identity is not eligible for migration", http.StatusForbidden)
		return
	}
	if err := h.Service.MigrateOwner(r.Context(), destination, in.OldID); errors.Is(err, ErrInvalidOwnerMigration) {
		http.Error(w, "source identity is not eligible for migration", http.StatusForbidden)
		return
	} else if err != nil {
		http.Error(w, "app owner migration failed", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Migration started"})
}

func (h Handler) Update(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	in, err := decodeMutation(r)
	if err != nil {
		http.Error(w, "invalid app_data", 422)
		return
	}
	err = h.Service.Update(r.Context(), uid, r.PathValue("app_id"), in)
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	err = h.Service.Delete(r.Context(), uid, r.PathValue("app_id"))
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
func (h Handler) Visibility(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	private, err := strconv.ParseBool(r.URL.Query().Get("private"))
	if err != nil {
		http.Error(w, "private must be boolean", 422)
		return
	}
	err = h.Service.SetVisibility(r.Context(), uid, r.PathValue("app_id"), private)
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
func (h Handler) Public(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.Public(r.Context(), strings.HasSuffix(r.URL.Path, "/popular"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, items)
}
func (h Handler) AdminToggle(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("secret-key")
	if key == "" {
		key = r.Header.Get("X-Admin-Key")
	}
	if key != os.Getenv("ADMIN_KEY") {
		http.Error(w, "forbidden", 403)
		return
	}
	id := r.PathValue("app_id")
	var err error
	switch {
	case strings.HasSuffix(r.URL.Path, "/approve"):
		err = h.Service.SetApproved(r.Context(), id, true)
	case strings.HasSuffix(r.URL.Path, "/reject"):
		err = h.Service.SetApproved(r.Context(), id, false)
	case strings.HasSuffix(r.URL.Path, "/popular"):
		v, _ := strconv.ParseBool(r.URL.Query().Get("value"))
		err = h.Service.SetPopular(r.Context(), id, v)
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}

func (h Handler) Unapproved(w http.ResponseWriter, r *http.Request) {
	if !adminAuthorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT `+appColumns+` FROM plugins_data WHERE approved=0 AND private=0 ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, e := scanApp(rows)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, out)
}
func (h Handler) PersonaAdmin(w http.ResponseWriter, r *http.Request) {
	if !adminAuthorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	id := r.PathValue("persona_id")
	if r.Method == http.MethodDelete {
		res, err := h.Service.DB.ExecContext(r.Context(), `DELETE FROM plugins_data WHERE id=? AND JSON_CONTAINS(capabilities,JSON_QUOTE('persona'))`, id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			http.Error(w, "Persona not found", 404)
			return
		}
		_ = writeJSON(w, map[string]string{"status": "ok"})
		return
	}
	rows, err := h.Service.DB.QueryContext(r.Context(), `SELECT `+appColumns+` FROM plugins_data WHERE (id=? OR author=?) AND JSON_CONTAINS(capabilities,JSON_QUOTE('persona'))`, id, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, e := scanApp(rows)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		http.Error(w, "Persona not found", 404)
		return
	}
	_ = writeJSON(w, out)
}

func (h Handler) Persona(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	item, err := h.Service.Persona(r.Context(), uid)
	if errors.Is(err, ErrAppNotFound) && r.Method == http.MethodPost {
		name := "My Persona"
		if in, decodeErr := decodeMutation(r); decodeErr == nil && in.Name != nil && strings.TrimSpace(*in.Name) != "" {
			name = strings.TrimSpace(*in.Name)
		}
		caps, private, category := []string{"persona"}, true, "personality-emulation"
		item, err = h.Service.Create(r.Context(), uid, MutationInput{Name: &name, Capabilities: &caps, Private: &private, Category: &category})
	}
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, item)
}

func catalogItem(item App) map[string]any {
	return map[string]any{"id": item.ID, "name": item.Name, "description": item.Description, "image": item.Image, "category": item.Category, "author": item.Author, "capabilities": item.Capabilities, "approved": item.Approved, "status": item.Status, "private": item.Private, "installs": item.Installs, "rating_avg": item.RatingAvg, "rating_count": item.RatingCount, "external_integration": item.ExternalIntegration, "is_paid": item.IsPaid, "price": item.Price, "enabled": item.Enabled}
}

func pagination(total, offset, limit int) map[string]any {
	count := total - offset
	if count < 0 {
		count = 0
	}
	if count > limit {
		count = limit
	}
	return map[string]any{"total": total, "count": count, "offset": offset, "limit": limit, "hasNext": offset+limit < total, "hasPrevious": offset > 0}
}

func (h Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	offset, limit := boundedPage(r.URL.Query().Get("offset"), r.URL.Query().Get("limit"))
	capability, category := r.URL.Query().Get("capability"), r.URL.Query().Get("category")
	if capability == "" && strings.Contains(r.URL.Path, "/capability/") {
		capability = r.PathValue("capability_id")
	}
	items, err := h.Service.List(r.Context(), "", false)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	filtered := filterCatalog(items, category, capability)
	if capability != "" || category != "" {
		page := sliceApps(filtered, offset, limit)
		payload := map[string]any{"data": mapsCatalog(page), "pagination": pagination(len(filtered), offset, limit)}
		if capability != "" {
			payload["capability"] = map[string]string{"id": capability, "title": capabilityTitle(capability)}
		}
		if category != "" {
			payload["category"] = map[string]string{"id": category, "title": categoryTitle(category)}
		}
		_ = writeJSON(w, payload)
		return
	}
	groups := []map[string]any{}
	for _, cap := range Capabilities() {
		if cap.ID == "memories" {
			cap.ID = "memories"
		}
		groupItems := filterCatalog(filtered, "", cap.ID)
		if len(groupItems) == 0 {
			continue
		}
		groups = append(groups, map[string]any{"capability": map[string]string{"id": cap.ID, "title": cap.Title}, "data": mapsCatalog(sliceApps(groupItems, offset, limit)), "pagination": pagination(len(groupItems), offset, limit)})
	}
	_ = writeJSON(w, map[string]any{"groups": groups, "meta": map[string]any{"capabilities": Capabilities(), "groupCount": len(groups), "limit": limit, "offset": offset}})
}

func (h Handler) Search(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	offset, limit := boundedPage(r.URL.Query().Get("offset"), r.URL.Query().Get("limit"))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	category, capability, sort := r.URL.Query().Get("category"), r.URL.Query().Get("capability"), r.URL.Query().Get("sort")
	items, err := h.Service.List(r.Context(), uid, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	filtered := filterCatalog(items, category, capability)
	installed := r.URL.Query().Get("installed_apps") == "true"
	mine := r.URL.Query().Get("my_apps") == "true"
	if installed || mine {
		next := []App{}
		enabled, _ := h.Service.Enabled(r.Context(), uid)
		enabledSet := map[string]bool{}
		for _, id := range enabled {
			enabledSet[id] = true
		}
		for _, item := range filtered {
			if (installed && enabledSet[item.ID]) || (mine && item.UID != nil && *item.UID == uid) {
				next = append(next, item)
			}
		}
		filtered = next
	}
	if query != "" {
		next := []App{}
		for _, item := range filtered {
			if strings.Contains(strings.ToLower(item.Name), query) || strings.Contains(strings.ToLower(item.Description), query) {
				next = append(next, item)
			}
		}
		filtered = next
	}
	sortApps(filtered, sort)
	page := sliceApps(filtered, offset, limit)
	_ = writeJSON(w, map[string]any{"data": mapsCatalog(page), "pagination": pagination(len(filtered), offset, limit), "filters": map[string]any{"query": queryOrNil(query), "category": queryOrNil(category), "capability": queryOrNil(capability), "sort": sortOrDefault(sort), "my_apps": mine, "installed_apps": installed}})
}

func (h Handler) Reviews(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.Reviews(r.Context(), r.PathValue("app_id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, items)
}
func (h Handler) Review(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		appID = r.PathValue("app_id")
	}
	var input struct {
		Score    float64 `json:"score"`
		Review   *string `json:"review"`
		Username *string `json:"username"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	text, name := "", ""
	if input.Review != nil {
		text = *input.Review
	}
	if input.Username != nil {
		name = *input.Username
	}
	update := strings.Contains(r.URL.Path, "/review") && r.PathValue("app_id") != ""
	if err = h.Service.SaveReview(r.Context(), uid, appID, input.Score, text, name, update); err != nil {
		if errors.Is(err, ErrAppNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
func (h Handler) Reply(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var input struct {
		ReviewerUID string `json:"reviewer_uid"`
		Response    string `json:"response"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.ReviewerUID == "" {
		http.Error(w, "invalid JSON", 422)
		return
	}
	if err = h.Service.ReplyReview(r.Context(), uid, r.PathValue("app_id"), input.ReviewerUID, input.Response); err != nil {
		if errors.Is(err, ErrAppNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), 400)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}

func boundedPage(rawOffset, rawLimit string) (int, int) {
	offset, _ := strconv.Atoi(rawOffset)
	limit, _ := strconv.Atoi(rawLimit)
	if offset < 0 {
		offset = 0
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return offset, limit
}
func queryOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func sortOrDefault(value string) string {
	if value == "" {
		return "installs"
	}
	return value
}
func mapsCatalog(items []App) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, catalogItem(item))
	}
	return out
}
func sliceApps(items []App, offset, limit int) []App {
	if offset >= len(items) {
		return []App{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
func filterCatalog(items []App, category, capability string) []App {
	out := []App{}
	for _, item := range items {
		if category != "" && item.Category != category {
			continue
		}
		if capability != "" && !contains(item.Capabilities, capability) {
			continue
		}
		out = append(out, item)
	}
	return out
}
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func sortApps(items []App, order string) {
	sort.SliceStable(items, func(i, j int) bool {
		switch order {
		case "name_asc":
			return items[i].Name < items[j].Name
		case "name_desc":
			return items[i].Name > items[j].Name
		case "rating_asc":
			return ratingValue(items[i]) < ratingValue(items[j])
		case "rating_desc":
			return ratingValue(items[i]) > ratingValue(items[j])
		default:
			return items[i].Installs > items[j].Installs
		}
	})
}
func ratingValue(item App) float64 {
	if item.RatingAvg == nil {
		return 0
	}
	return *item.RatingAvg
}
func capabilityTitle(id string) string {
	for _, item := range Capabilities() {
		if item.ID == id {
			return item.Title
		}
	}
	return id
}
func categoryTitle(id string) string {
	for _, item := range Categories() {
		if item.ID == id {
			return item.Title
		}
	}
	return id
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	items, err := h.Service.List(r.Context(), uid, true)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, items)
}
func (h Handler) Enabled(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	items, err := h.Service.Enabled(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, items)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	item, err := h.Service.Get(r.Context(), uid, r.PathValue("app_id"))
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, item)
}
func (h Handler) Toggle(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("app_id"))
	if id == "" {
		http.Error(w, "app_id is required", 400)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/disable") || r.URL.Query().Get("enabled") == strconv.FormatBool(false) {
		err = h.Service.Disable(r.Context(), uid, id)
	} else {
		err = h.Service.Enable(r.Context(), uid, id)
	}
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}

func (h Handler) Generate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Category    string `json:"category"`
	}
	if r.Method != http.MethodGet {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid JSON", 422)
			return
		}
	}
	path := r.URL.Path
	if path == "/v1/app/generate-icon" {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "invalid JSON", 422)
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			http.Error(w, "App name is required", 422)
			return
		}
		if strings.TrimSpace(in.Description) == "" {
			http.Error(w, "App description is required", 422)
			return
		}
		key := os.Getenv("OPENAI_API_KEY")
		endpoint := os.Getenv("OPENAI_IMAGE_ENDPOINT")
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1/images/generations"
		}
		if key == "" {
			http.Error(w, "image provider is not configured", 503)
			return
		}
		payload, _ := json.Marshal(map[string]any{"model": envDefault("OPENAI_IMAGE_MODEL", "gpt-image-1"), "prompt": fmt.Sprintf("Create a clean app icon for %s, %s, category %s", in.Name, in.Description, in.Category), "size": "1024x1024", "response_format": "b64_json"})
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, strings.NewReader(string(payload)))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			http.Error(w, "image provider request failed", 502)
			return
		}
		var result struct {
			Data []struct {
				B64 string `json:"b64_json"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &result) != nil || len(result.Data) == 0 || result.Data[0].B64 == "" {
			http.Error(w, "image provider returned no image", 502)
			return
		}
		_ = writeJSON(w, map[string]string{"status": "ok", "icon_base64": result.Data[0].B64, "mime_type": "image/png"})
		return
	}
	if h.Provider == nil {
		http.Error(w, "app generator provider is not configured", http.StatusServiceUnavailable)
		return
	}
	if path == "/v1/app/generate-description" {
		if strings.TrimSpace(in.Name) == "" {
			http.Error(w, "App Name is required", 422)
			return
		}
		if strings.TrimSpace(in.Description) == "" {
			http.Error(w, "App Description is required", 422)
			return
		}
		answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "user", Content: fmt.Sprintf("Improve this app description in no more than 40 words. App name: %s. Draft: %s. Return only the description.", in.Name, in.Description)}})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		_ = writeJSON(w, map[string]string{"description": strings.TrimSpace(answer)})
		return
	}
	if path == "/v1/app/generate-description-emoji" {
		if strings.TrimSpace(in.Name) == "" {
			http.Error(w, "App Name is required", 422)
			return
		}
		if strings.TrimSpace(in.Prompt) == "" {
			http.Error(w, "App Prompt is required", 422)
			return
		}
		answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Return only JSON with keys description and emoji. Description must be at most 40 words and emoji must be one emoji."}, {Role: "user", Content: "App name: " + in.Name + "; purpose: " + in.Prompt}})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		var out struct {
			Description string `json:"description"`
			Emoji       string `json:"emoji"`
		}
		if json.Unmarshal([]byte(stripJSONFence(answer)), &out) != nil || out.Description == "" {
			out.Description = "A custom app that " + in.Prompt
		}
		if out.Emoji == "" {
			out.Emoji = "✨"
		}
		_ = writeJSON(w, out)
		return
	}
	if path == "/v1/app/generate-prompts" {
		answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Return exactly a JSON array of five short creative app ideas. The first three analyze conversations and the last two are chat assistants. Return JSON only."}, {Role: "user", Content: "Generate the ideas now."}})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		var prompts []string
		if json.Unmarshal([]byte(stripJSONFence(answer)), &prompts) != nil || len(prompts) < 5 {
			prompts = []string{"Mind map generator from conversations", "Jokes and funny moments extractor", "Key decisions and commitments tracker", "Startup advisor clone", "Strict accountability coach"}
		}
		_ = writeJSON(w, map[string]any{"prompts": prompts[:5]})
		return
	}
	if path == "/v1/app/generate" {
		prompt := strings.TrimSpace(in.Prompt)
		if prompt == "" {
			http.Error(w, "Prompt is required", 422)
			return
		}
		if len([]rune(prompt)) < 10 {
			http.Error(w, "Prompt is too short. Please provide more details.", 422)
			return
		}
		if len([]rune(prompt)) > 2000 {
			http.Error(w, "Prompt is too long. Please keep it under 2000 characters.", 422)
			return
		}
		answer, err := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Return only JSON with keys name, description, category, capabilities (array), chat_prompt, memory_prompt. Generate an app draft from the user's request."}, {Role: "user", Content: prompt}})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		var app map[string]any
		if json.Unmarshal([]byte(stripJSONFence(answer)), &app) != nil {
			http.Error(w, "failed to generate app", 500)
			return
		}
		_ = writeJSON(w, map[string]any{"status": "ok", "app": app})
		return
	}
	http.NotFound(w, r)
}

func (h Handler) Thumbnail(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if h.ThumbnailStore == nil {
		http.Error(w, "app thumbnail storage is not configured", 503)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "invalid multipart upload", 422)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", 422)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	if len(data) > 10<<20 {
		http.Error(w, "file is too large", 413)
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	if !strings.HasPrefix(contentType, "image/") {
		http.Error(w, "file must be an image", 422)
		return
	}
	id := newID()
	name := id + "-" + filepath.Base(header.Filename)
	url, err := h.ThumbnailStore.Put(r.Context(), uid, name, contentType, data)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	_ = writeJSON(w, map[string]string{"thumbnail_url": url, "thumbnail_id": id})
}

func rapidAPIJSON(ctx context.Context, endpoint, handle string) (map[string]any, error) {
	host, key := os.Getenv("RAPID_API_HOST"), os.Getenv("RAPID_API_KEY")
	if host == "" || key == "" {
		return nil, errors.New("RAPID_API_KEY/RAPID_API_HOST not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+endpoint+url.QueryEscape(handle), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-RapidAPI-Key", key)
	req.Header.Set("X-RapidAPI-Host", host)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RapidAPI returned %s", resp.Status)
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("invalid RapidAPI response")
	}
	if status, _ := out["status"].(string); status == "error" {
		return nil, errors.New("RapidAPI returned an error")
	}
	return out, nil
}
func (h Handler) Twitter(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	handle := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("handle")), "@")
	username := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("username")), "@")
	if strings.HasSuffix(r.URL.Path, "/profile") {
		if handle == "" {
			http.Error(w, "handle is required", 422)
			return
		}
		data, err := rapidAPIJSON(r.Context(), "/screenname.php?screenname=", handle)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		avatar, _ := data["avatar"].(string)
		avatar = strings.ReplaceAll(avatar, "_normal", "")
		out := map[string]any{"name": data["name"], "profile": data["profile"], "rest_id": data["rest_id"], "avatar": avatar, "desc": data["desc"], "friends": data["friends"], "sub_count": data["sub_count"], "id": data["id"], "status": data["status"]}
		var id string
		var name string
		if h.Service.DB.QueryRowContext(r.Context(), `SELECT id,name FROM plugins_data WHERE uid=? AND JSON_UNQUOTE(JSON_EXTRACT(external_integration,'$.twitter.username'))=? LIMIT 1`, uid, handle).Scan(&id, &name) == nil {
			out["persona_id"] = id
			out["persona_username"] = name
		}
		_ = writeJSON(w, out)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/verify-ownership") {
		if username == "" || handle == "" {
			http.Error(w, "username and handle are required", 422)
			return
		}
		data, err := rapidAPIJSON(r.Context(), "/timeline.php?screenname=", handle)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		tweet := ""
		if list, ok := data["timeline"].([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				tweet, _ = first["text"].(string)
			}
		}
		verified := strings.Contains(tweet, "Verifying my clone("+username+")")
		out := map[string]any{"tweet": tweet, "verified": verified}
		if verified && r.URL.Query().Get("persona_id") != "" {
			out["persona_id"] = r.URL.Query().Get("persona_id")
		}
		_ = writeJSON(w, out)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/initial-message") {
		if username == "" {
			http.Error(w, "username is required", 422)
			return
		}
		var name, description string
		_ = h.Service.DB.QueryRowContext(r.Context(), `SELECT name,description FROM plugins_data WHERE name=? OR JSON_UNQUOTE(JSON_EXTRACT(external_integration,'$.twitter.username'))=? LIMIT 1`, username, username).Scan(&name, &description)
		if name == "" {
			_ = writeJSON(w, map[string]string{"message": ""})
			return
		}
		if h.Provider == nil {
			http.Error(w, "chat provider is not configured", 503)
			return
		}
		msg, e := h.Provider.Complete(r.Context(), []chat.Turn{{Role: "system", Content: "Write a concise first-person introductory message for a Twitter persona."}, {Role: "user", Content: "Persona name: " + name + ". Description: " + description}})
		if e != nil {
			http.Error(w, e.Error(), 502)
			return
		}
		_ = writeJSON(w, map[string]string{"message": strings.TrimSpace(msg)})
		return
	}
	http.NotFound(w, r)
}

func stripJSONFence(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		lines := strings.Split(value, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return value
}
func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func adminAuthorized(r *http.Request) bool {
	key := r.Header.Get("secret-key")
	if key == "" {
		key = r.Header.Get("X-Admin-Key")
	}
	return key != "" && key == os.Getenv("ADMIN_KEY")
}
func (h Handler) AppAPIKeys(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	id := r.PathValue("app_id")
	switch r.Method {
	case http.MethodPost:
		k, err := h.Service.CreateAPIKey(r.Context(), uid, id)
		if errors.Is(err, ErrAppNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		_ = writeJSON(w, k)
	case http.MethodGet:
		keys, err := h.Service.ListAPIKeys(r.Context(), uid, id)
		if errors.Is(err, ErrAppNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		_ = writeJSON(w, keys)
	case http.MethodDelete:
		err := h.Service.DeleteAPIKey(r.Context(), uid, id, r.PathValue("key_id"))
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "API key not found", 404)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		_ = writeJSON(w, map[string]string{"status": "ok", "message": "API key deleted"})
	}
}

func (h Handler) RefreshManifest(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	app, err := h.Service.Get(r.Context(), uid, r.PathValue("app_id"))
	if errors.Is(err, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if app.UID == nil || *app.UID != uid {
		http.Error(w, "forbidden", 403)
		return
	}
	raw, ok := app.ExternalIntegration["chat_tools_manifest_url"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		http.Error(w, "App does not have a chat tools manifest URL", 400)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		http.Error(w, "invalid manifest URL", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "failed to fetch manifest", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "failed to fetch manifest", 502)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		http.Error(w, "failed to read manifest", 502)
		return
	}
	var manifest struct {
		Tools        []map[string]any `json:"tools"`
		ChatMessages map[string]any   `json:"chat_messages"`
	}
	if json.Unmarshal(body, &manifest) != nil {
		http.Error(w, "invalid manifest JSON", 502)
		return
	}
	base, _ := app.ExternalIntegration["app_home_url"].(string)
	for _, tool := range manifest.Tools {
		if ep, ok := tool["endpoint"].(string); ok && strings.HasPrefix(ep, "/") && base != "" {
			tool["endpoint"] = strings.TrimRight(base, "/") + ep
		}
	}
	ext := map[string]any{}
	for k, v := range app.ExternalIntegration {
		ext[k] = v
	}
	if manifest.ChatMessages != nil {
		for k, v := range manifest.ChatMessages {
			ext["chat_messages_"+k] = v
		}
	} else {
		ext["chat_messages_enabled"] = false
		ext["chat_messages_target"] = "app"
		ext["chat_messages_notify"] = false
	}
	b, _ := json.Marshal(ext)
	tools, _ := json.Marshal(manifest.Tools)
	_, err = h.Service.DB.ExecContext(r.Context(), `UPDATE plugins_data SET chat_tools=?,external_integration=?,updated_at=NOW(6) WHERE id=?`, tools, b, app.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, map[string]any{"status": "ok", "tools_count": len(manifest.Tools)})
}

func discoverMCP(ctx context.Context, endpoint string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	payload := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": map[string]any{}}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP server returned %s", resp.Status)
	}
	var result struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, errors.New("MCP tools/list returned an error")
	}
	return result.Result.Tools, nil
}
func (h Handler) MCP(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/mcp" {
		var in struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			MCPServerURL string `json:"mcp_server_url"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.MCPServerURL) == "" {
			http.Error(w, "name and mcp_server_url are required", 422)
			return
		}
		u, e := url.Parse(in.MCPServerURL)
		if e != nil || u.Scheme != "http" && u.Scheme != "https" {
			http.Error(w, "invalid MCP server URL", 422)
			return
		}
		tools, e := discoverMCP(r.Context(), in.MCPServerURL)
		if e != nil {
			http.Error(w, "failed to discover MCP tools", 502)
			return
		}
		if len(tools) == 0 {
			http.Error(w, "no tools found on the MCP server", 422)
			return
		}
		id := newID()
		ext, _ := json.Marshal(map[string]any{"mcp_server_url": strings.TrimRight(in.MCPServerURL, "/")})
		tb, _ := json.Marshal(tools)
		_, e = h.Service.DB.ExecContext(r.Context(), `INSERT INTO plugins_data (id,name,uid,private,approved,status,category,description,image,capabilities,external_integration,chat_tools,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NOW(6),NOW(6))`, id, in.Name, uid, true, true, "approved", "utilities-and-tools", in.Description, "", `["chat"]`, ext, tb)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		_ = writeJSON(w, map[string]any{"app_id": id, "requires_oauth": false, "tools_count": len(tools), "tool_names": toolNames(tools)})
		return
	}
	id := r.PathValue("app_id")
	app, e := h.Service.Get(r.Context(), uid, id)
	if errors.Is(e, ErrAppNotFound) {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if app.UID == nil || *app.UID != uid {
		http.Error(w, "forbidden", 403)
		return
	}
	raw, _ := app.ExternalIntegration["mcp_server_url"].(string)
	if raw == "" {
		http.Error(w, "App is not an MCP server app", 422)
		return
	}
	tools, e := discoverMCP(r.Context(), raw)
	if e != nil {
		http.Error(w, "failed to discover tools", 502)
		return
	}
	tb, _ := json.Marshal(tools)
	if _, e = h.Service.DB.ExecContext(r.Context(), `UPDATE plugins_data SET chat_tools=?,updated_at=NOW(6) WHERE id=?`, tb, id); e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_ = writeJSON(w, map[string]any{"tools_count": len(tools), "tool_names": toolNames(tools)})
}
func toolNames(tools []map[string]any) []string {
	out := []string{}
	for _, t := range tools {
		if n, ok := t["name"].(string); ok {
			out = append(out, n)
		}
	}
	return out
}
func (h Handler) Testers(w http.ResponseWriter, r *http.Request) {
	if !adminAuthorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	var in struct {
		UID   string   `json:"uid"`
		AppID string   `json:"app_id"`
		Apps  []string `json:"apps"`
	}
	if r.Method != http.MethodGet && json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid JSON", 422)
		return
	}
	if in.UID == "" {
		http.Error(w, "uid is required", 422)
		return
	}
	if r.URL.Path == "/v1/apps/tester" {
		if len(in.Apps) == 0 {
			http.Error(w, "apps is required", 422)
			return
		}
		for _, id := range in.Apps {
			if err := h.Service.SetTester(r.Context(), in.UID, id, true); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
	} else {
		if in.AppID == "" {
			http.Error(w, "app_id is required", 422)
			return
		}
		if err := h.Service.SetTester(r.Context(), in.UID, in.AppID, r.Method == http.MethodPost); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
func (h Handler) TesterCheck(w http.ResponseWriter, r *http.Request) {
	uid, err := auth.UserID(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	_ = writeJSON(w, map[string]bool{"is_tester": h.Service.IsTester(r.Context(), uid)})
}
func (h Handler) SummaryIDs(w http.ResponseWriter, r *http.Request) {
	if !adminAuthorized(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	id := r.PathValue("app_id")
	if r.Method == http.MethodGet {
		v, err := h.Service.SummaryIDs(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = writeJSON(w, map[string]any{"app_ids": v})
		return
	}
	err := h.Service.SetSummaryID(r.Context(), id, r.Method == http.MethodPost)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "app not found in conversation summary apps", 404)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = writeJSON(w, map[string]string{"status": "ok"})
}
