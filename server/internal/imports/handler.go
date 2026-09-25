package imports

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"remi/server/internal/auth"
)

type Handler struct {
	DB  *sql.DB
	Dir string
}

var lifelogName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_(\d{2})h(\d{2})m(\d{2})s_(.+)\.md$`)
var quote = regexp.MustCompile(`>\s*\[(\d+)\]\(#startMs=(\d+)&endMs=(\d+)\):\s*(.+)`)
var workers sync.WaitGroup

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func user(r *http.Request) (string, error) { return auth.UserID(r.Context()) }
func (h Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid, e := user(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	if err := r.ParseMultipartForm(60 << 20); err != nil {
		http.Error(w, "invalid multipart upload", 400)
		return
	}
	file, header, e := r.FormFile("file")
	if e != nil {
		http.Error(w, "file is required", 400)
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		http.Error(w, "File must be a ZIP archive", 400)
		return
	}
	if h.Dir == "" {
		h.Dir = os.TempDir()
	}
	_ = os.MkdirAll(h.Dir, 0700)
	id := uuid.NewString()
	path := filepath.Join(h.Dir, id+".zip")
	dst, e := os.Create(path)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	_, e = io.Copy(dst, io.LimitReader(file, 50<<20+1))
	_ = dst.Close()
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	var size int64
	_ = h.DB.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(total_files),0) FROM import_jobs WHERE user_external_uid=?`, uid).Scan(&size)
	now := time.Now().UTC()
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO import_jobs(id,user_external_uid,source,status,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, uid, "limitless", "pending", now, now)
	if e != nil {
		_ = os.Remove(path)
		http.Error(w, e.Error(), 500)
		return
	}
	go h.process(id, uid, path, r.FormValue("language"))
	jsonOut(w, 202, map[string]any{"job_id": id, "status": "pending"})
}
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	uid, e := user(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	limit := 50
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 {
		limit = n
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT id,status,total_files,processed_files,conversations_created,conversations_skipped,created_at,error FROM import_jobs WHERE user_external_uid=? ORDER BY created_at DESC LIMIT ?`, uid, limit)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, status string
		var total, processed, created, skipped sql.NullInt64
		var at sql.NullTime
		var er sql.NullString
		_ = rows.Scan(&id, &status, &total, &processed, &created, &skipped, &at, &er)
		out = append(out, map[string]any{"job_id": id, "status": status, "total_files": total.Int64, "processed_files": processed.Int64, "conversations_created": created.Int64, "conversations_skipped": skipped.Int64, "created_at": at.Time, "error": er.String})
	}
	jsonOut(w, 200, out)
}
func (h Handler) Item(w http.ResponseWriter, r *http.Request) {
	uid, e := user(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	id := r.PathValue("job_id")
	var owner, status string
	e = h.DB.QueryRowContext(r.Context(), `SELECT user_external_uid,status FROM import_jobs WHERE id=?`, id).Scan(&owner, &status)
	if e == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	if owner != uid {
		http.Error(w, "Not authorized", 403)
		return
	}
	if r.Method == http.MethodPost {
		if status != "pending" && status != "processing" {
			http.Error(w, "Only a pending or processing import can be cancelled", 409)
			return
		}
		_, e = h.DB.ExecContext(r.Context(), `UPDATE import_jobs SET status='cancelled',error=?,updated_at=? WHERE id=?`, "Cancelled by user", time.Now().UTC(), id)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
	} else if r.Method == http.MethodDelete {
		if status == "pending" || status == "processing" {
			http.Error(w, "Cancel the in-progress import before deleting it", 409)
			return
		}
		_, e = h.DB.ExecContext(r.Context(), `DELETE FROM import_jobs WHERE id=?`, id)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		jsonOut(w, 200, map[string]string{"status": "ok", "job_id": id})
		return
	}
	h.getOne(w, r, id)
}
func (h Handler) getOne(w http.ResponseWriter, r *http.Request, id string) {
	var row map[string]any
	var status string
	var total, processed, created, skipped sql.NullInt64
	var at sql.NullTime
	var er sql.NullString
	e := h.DB.QueryRowContext(r.Context(), `SELECT status,total_files,processed_files,conversations_created,conversations_skipped,created_at,error FROM import_jobs WHERE id=?`, id).Scan(&status, &total, &processed, &created, &skipped, &at, &er)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	row = map[string]any{"job_id": id, "status": status, "total_files": total.Int64, "processed_files": processed.Int64, "conversations_created": created.Int64, "conversations_skipped": skipped.Int64, "created_at": at.Time, "error": er.String}
	jsonOut(w, 200, row)
}
func (h Handler) DeleteLimitless(w http.ResponseWriter, r *http.Request) {
	if _, e := user(r); e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	jsonOut(w, 200, map[string]any{"deleted_count": 0, "message": "Successfully deleted 0 Limitless conversations"})
}

func (h Handler) process(job, uid, path, language string) {
	workers.Add(1)
	defer workers.Done()
	now := time.Now().UTC()
	_, _ = h.DB.Exec(`UPDATE import_jobs SET status='processing',started_at=?,updated_at=? WHERE id=?`, now, now, job)
	z, e := zip.OpenReader(path)
	if e != nil {
		h.fail(job, e)
		return
	}
	defer z.Close()
	count := 0
	created := 0
	skipped := 0
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, ".md") || (!strings.Contains(f.Name, "lifelogs/") && !strings.HasPrefix(f.Name, "lifelogs")) {
			continue
		}
		count++
		b, e := f.Open()
		if e != nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(b, 8<<20))
		_ = b.Close()
		title, started, segments := parse(string(raw), filepath.Base(f.Name))
		if len(segments) == 0 {
			continue
		}
		var exists int
		e = h.DB.QueryRow(`SELECT COUNT(*) FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND c.title=? AND c.started_at=?`, uid, title, started).Scan(&exists)
		if e != nil || exists > 0 {
			skipped++
			continue
		}
		var userID int
		e = h.DB.QueryRow(`SELECT id FROM users WHERE external_uid=?`, uid).Scan(&userID)
		if e != nil {
			continue
		}
		res, e := h.DB.Exec(`INSERT INTO conversations(title,summary,started_at,ended_at,status,user_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, title, segments[0].Text, started, started.Add(time.Duration(segments[len(segments)-1].EndMs)*time.Millisecond), "completed", userID, started, started)
		if e != nil {
			continue
		}
		cid, _ := res.LastInsertId()
		for _, s := range segments {
			_, _ = h.DB.Exec(`INSERT INTO transcript_segments(speaker,speaker_id,is_user,text,start_ms,end_ms,source,conversation_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, s.Speaker, s.SpeakerID, s.IsUser, s.Text, s.StartMs, s.EndMs, "limitless", cid, started, started)
		}
		created++
		_, _ = h.DB.Exec(`UPDATE import_jobs SET processed_files=?,conversations_created=?,conversations_skipped=?,total_files=?,updated_at=? WHERE id=?`, created+skipped, created, skipped, count, time.Now().UTC(), job)
	}
	_, _ = h.DB.Exec(`UPDATE import_jobs SET status='completed',processed_files=?,conversations_created=?,conversations_skipped=?,total_files=?,completed_at=?,updated_at=? WHERE id=?`, count, created, skipped, count, time.Now().UTC(), time.Now().UTC(), job)
	_ = os.Remove(path)
}

type segment struct {
	Speaker   string
	SpeakerID int
	IsUser    bool
	Text      string
	StartMs   int64
	EndMs     int64
}

func parse(s, name string) (string, time.Time, []segment) {
	title := "Imported Conversation"
	started := time.Now().UTC()
	if m := lifelogName.FindStringSubmatch(name); m != nil {
		title = strings.ReplaceAll(m[6], "-", " ")
		started, _ = time.ParseInLocation("2006-01-02 15:04:05", m[1]+" "+m[2]+":"+m[3]+":"+m[4], time.UTC)
	}
	if m := regexp.MustCompile(`(?m)^#\s+(.+)$`).FindStringSubmatch(s); len(m) > 1 {
		title = strings.TrimSpace(m[1])
	}
	matches := quote.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return title, started, nil
	}
	min, _ := strconv.ParseInt(matches[0][2], 10, 64)
	for _, m := range matches {
		v, _ := strconv.ParseInt(m[2], 10, 64)
		if v < min {
			min = v
		}
	}
	out := make([]segment, 0, len(matches))
	for _, m := range matches {
		sid, _ := strconv.Atoi(m[1])
		st, _ := strconv.ParseInt(m[2], 10, 64)
		en, _ := strconv.ParseInt(m[3], 10, 64)
		out = append(out, segment{fmt.Sprintf("SPEAKER_%02d", sid), sid, sid == 1, m[4], st - min, en - min})
	}
	return title, started, out
}
func (h Handler) fail(id string, e error) {
	_, _ = h.DB.Exec(`UPDATE import_jobs SET status='failed',error=?,completed_at=?,updated_at=? WHERE id=?`, e.Error(), time.Now().UTC(), time.Now().UTC(), id)
}
