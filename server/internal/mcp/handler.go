package mcp

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/mcpkeys"
)

type Handler struct {
	DB   *sql.DB
	Keys mcpkeys.Service
}

func (h Handler) uid(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	raw = strings.TrimPrefix(raw, "Bearer ")
	if raw == "" {
		return "", errors.New("invalid or missing API key")
	}
	return h.Keys.Authenticate(r.Context(), raw)
}

func (h Handler) auth(r *http.Request, required ...string) (string, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
	ctx, err := h.Keys.AuthenticateContext(r.Context(), raw)
	if err != nil {
		return "", err
	}
	for _, wanted := range required {
		allowed := false
		for _, scope := range ctx.Scopes {
			if scope == wanted {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", errors.New("insufficient MCP scope: " + wanted)
		}
	}
	return ctx.UID, nil
}
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, err error) { http.Error(w, err.Error(), code) }
func page(r *http.Request, def, max int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit < 1 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (h Handler) Memories(w http.ResponseWriter, r *http.Request) {
	uid, err := h.auth(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	id := r.PathValue("memory_id")
	if r.Method == http.MethodPost {
		var in struct {
			Content    string `json:"content"`
			Category   string `json:"category"`
			Type       string `json:"type"`
			Importance int    `json:"importance"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Content) == "" {
			fail(w, 422, errors.New("content is required"))
			return
		}
		if in.Importance == 0 {
			in.Importance = 50
		}
		result, err := h.DB.ExecContext(r.Context(), `INSERT INTO memories (type,category,content,importance,created_at,updated_at,user_id) SELECT ?,?,?,?,?,u.id FROM users u WHERE u.external_uid=?`, defaultText(in.Type, "fact"), defaultText(in.Category, "interesting"), in.Content, in.Importance, time.Now().UTC(), time.Now().UTC(), uid)
		if err != nil {
			fail(w, 500, err)
			return
		}
		newID, _ := result.LastInsertId()
		jsonOut(w, map[string]any{"id": strconv.FormatInt(newID, 10), "content": in.Content, "category": defaultText(in.Category, "interesting"), "type": defaultText(in.Type, "fact"), "importance": in.Importance, "memory_default_memory": true})
		return
	}
	if id != "" {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/complete") {
			completed := true
			if raw := r.URL.Query().Get("completed"); raw != "" {
				completed, _ = strconv.ParseBool(raw)
			}
			status := "active"
			if completed {
				status = "completed"
			}
			res, e := h.DB.ExecContext(r.Context(), `UPDATE action_items a JOIN users u ON u.id=a.user_id SET a.status=?,a.completed_at=IF(?,NOW(6),NULL),a.updated_at=NOW(6) WHERE a.id=? AND u.external_uid=?`, status, completed, id, uid)
			if e != nil {
				fail(w, 500, e)
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				http.NotFound(w, r)
				return
			}
			jsonOut(w, map[string]any{"id": id, "completed": completed, "status": status})
			return
		}
		if r.Method == http.MethodDelete {
			res, err := h.DB.ExecContext(r.Context(), `DELETE m FROM memories m JOIN users u ON u.id=m.user_id WHERE m.id=? AND u.external_uid=?`, id, uid)
			if err != nil {
				fail(w, 500, err)
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				http.NotFound(w, r)
				return
			}
			jsonOut(w, map[string]string{"status": "ok"})
			return
		}
		if r.Method == http.MethodPatch {
			value := r.URL.Query().Get("value")
			if value == "" {
				var in struct {
					Value string `json:"value"`
				}
				if json.NewDecoder(r.Body).Decode(&in) == nil {
					value = in.Value
				}
			}
			res, err := h.DB.ExecContext(r.Context(), `UPDATE memories m JOIN users u ON u.id=m.user_id SET m.content=?,m.updated_at=NOW(6) WHERE m.id=? AND u.external_uid=?`, value, id, uid)
			if err != nil {
				fail(w, 500, err)
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				http.NotFound(w, r)
				return
			}
			jsonOut(w, map[string]string{"status": "ok"})
			return
		}
	}
	limit, offset := page(r, 25, 500)
	query := r.URL.Query().Get("query")
	var rows *sql.Rows
	if query != "" {
		rows, err = h.DB.QueryContext(r.Context(), `SELECT m.id,m.content,m.category,m.type,m.created_at,m.updated_at FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? AND m.content LIKE ? ORDER BY m.created_at DESC LIMIT ? OFFSET ?`, uid, "%"+query+"%", limit, offset)
	} else {
		rows, err = h.DB.QueryContext(r.Context(), `SELECT m.id,m.content,m.category,m.type,m.created_at,m.updated_at FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? ORDER BY m.created_at DESC LIMIT ? OFFSET ?`, uid, limit, offset)
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var content, category, typ string
		var created, updated time.Time
		if rows.Scan(&id, &content, &category, &typ, &created, &updated) == nil {
			out = append(out, map[string]any{"id": strconv.Itoa(id), "content": content, "category": category, "type": typ, "created_at": created, "updated_at": updated, "memory_default_memory": true})
		}
	}
	jsonOut(w, out)
}

func (h Handler) Conversations(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	id := r.PathValue("conversation_id")
	if id != "" {
		var title, summary, status string
		var started time.Time
		var ended sql.NullTime
		err = h.DB.QueryRowContext(r.Context(), `SELECT c.title,c.summary,c.status,c.started_at,c.ended_at FROM conversations c JOIN users u ON u.id=c.user_id WHERE c.id=? AND u.external_uid=? AND c.status='completed'`, id, uid).Scan(&title, &summary, &status, &started, &ended)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			fail(w, 500, err)
			return
		}
		item := map[string]any{"id": id, "started_at": started, "finished_at": nil, "structured": map[string]any{"title": title, "overview": summary, "category": "other"}, "language": nil, "apps_results": []any{}, "transcript_segments": []any{}}
		if ended.Valid {
			item["finished_at"] = ended.Time
		}
		jsonOut(w, item)
		return
	}
	limit, offset := page(r, 100, 1000)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT c.id,c.title,c.summary,c.started_at,c.ended_at FROM conversations c JOIN users u ON u.id=c.user_id WHERE u.external_uid=? AND c.status='completed' ORDER BY c.started_at DESC LIMIT ? OFFSET ?`, uid, limit, offset)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var title, summary string
		var started time.Time
		var ended sql.NullTime
		if rows.Scan(&id, &title, &summary, &started, &ended) == nil {
			out = append(out, map[string]any{"id": strconv.Itoa(id), "started_at": started, "finished_at": nullTime(ended), "structured": map[string]any{"title": title, "overview": summary, "category": "other"}, "apps_results": []any{}, "match_snippets": []any{}})
		}
	}
	jsonOut(w, out)
}

func (h Handler) ActionItems(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	id := r.PathValue("action_item_id")
	if id != "" {
		if r.Method == http.MethodDelete {
			res, e := h.DB.ExecContext(r.Context(), `DELETE a FROM action_items a JOIN users u ON u.id=a.user_id WHERE a.id=? AND u.external_uid=?`, id, uid)
			if e != nil {
				fail(w, 500, e)
				return
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				http.NotFound(w, r)
				return
			}
			jsonOut(w, map[string]string{"status": "ok"})
			return
		}
		if r.Method == http.MethodPatch {
			var in struct {
				Description *string    `json:"description"`
				DueAt       *time.Time `json:"due_at"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				fail(w, 422, errors.New("invalid JSON"))
				return
			}
			if in.Description != nil {
				_, err = h.DB.ExecContext(r.Context(), `UPDATE action_items a JOIN users u ON u.id=a.user_id SET a.description=?,a.updated_at=NOW(6) WHERE a.id=? AND u.external_uid=?`, *in.Description, id, uid)
			}
			if err != nil {
				fail(w, 500, err)
				return
			}
			jsonOut(w, map[string]string{"status": "ok"})
			return
		}
	}
	if r.Method == http.MethodPost {
		var in struct {
			Description string     `json:"description"`
			DueAt       *time.Time `json:"due_at"`
			Completed   bool       `json:"completed"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Description) == "" {
			fail(w, 422, errors.New("description is required"))
			return
		}
		status := "active"
		if in.Completed {
			status = "completed"
		}
		result, err := h.DB.ExecContext(r.Context(), `INSERT INTO action_items (description,status,due_at,created_at,updated_at,user_id) SELECT ?,?,?,NOW(6),NOW(6),u.id FROM users u WHERE u.external_uid=?`, in.Description, status, in.DueAt, uid)
		if err != nil {
			fail(w, 500, err)
			return
		}
		newID, _ := result.LastInsertId()
		jsonOut(w, map[string]any{"id": strconv.FormatInt(newID, 10), "description": in.Description, "completed": in.Completed, "due_at": in.DueAt})
		return
	}
	limit, offset := page(r, 100, 500)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT a.id,a.description,a.status,a.created_at,a.due_at,a.completed_at,a.conversation_id FROM action_items a JOIN users u ON u.id=a.user_id WHERE u.external_uid=? AND a.status<>'cancelled' ORDER BY a.created_at DESC LIMIT ? OFFSET ?`, uid, limit, offset)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int
		var desc, status string
		var created time.Time
		var due, completed sql.NullTime
		var conv sql.NullInt64
		if rows.Scan(&id, &desc, &status, &created, &due, &completed, &conv) == nil {
			out = append(out, map[string]any{"id": strconv.Itoa(id), "description": desc, "completed": status == "completed", "created_at": created, "due_at": nullTime(due), "completed_at": nullTime(completed), "conversation_id": nullInt(conv)})
		}
	}
	jsonOut(w, out)
}

func (h Handler) Goals(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT g.external_id,g.title,g.desired_outcome,g.status,g.created_at,g.updated_at FROM goals g JOIN users u ON u.id=g.user_id WHERE u.external_uid=? AND (? OR g.status NOT IN ('achieved','abandoned')) ORDER BY g.created_at DESC`, uid, r.URL.Query().Get("include_inactive") == "true")
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, outcome, status string
		var created, updated time.Time
		if rows.Scan(&id, &title, &outcome, &status, &created, &updated) == nil {
			out = append(out, map[string]any{"id": id, "title": title, "desired_outcome": outcome, "status": status, "created_at": created, "updated_at": updated})
		}
	}
	jsonOut(w, out)
}

func (h Handler) Chat(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	limit, offset := page(r, 50, 200)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT m.external_id,m.text,m.sender,m.type,m.created_at FROM chat_messages m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? ORDER BY m.created_at DESC LIMIT ? OFFSET ?`, uid, limit, offset)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, text, sender, typ string
		var created time.Time
		if rows.Scan(&id, &text, &sender, &typ, &created) == nil {
			out = append(out, map[string]any{"id": id, "text": text, "sender": sender, "type": typ, "created_at": created})
		}
	}
	jsonOut(w, out)
}

func (h Handler) People(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	rows, err := h.DB.QueryContext(r.Context(), `SELECT external_id,name,speech_sample_transcripts,created_at FROM people WHERE user_external_uid=? ORDER BY created_at DESC`, uid)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		var samples sql.NullString
		var created time.Time
		var arr []string
		if rows.Scan(&id, &name, &samples, &created) == nil {
			if samples.Valid {
				_ = json.Unmarshal([]byte(samples.String), &arr)
			}
			if arr == nil {
				arr = []string{}
			}
			out = append(out, map[string]any{"id": id, "name": name, "created_at": created, "speech_sample_transcripts": arr})
		}
	}
	jsonOut(w, out)
}

func (h Handler) ScreenActivity(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	limit, _ := page(r, 200, 200)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT storage_id,timestamp,app_name,window_title,ocr_text FROM screen_activity WHERE user_external_uid=? ORDER BY timestamp DESC LIMIT ?`, uid, limit)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, timestamp, app, window, ocr string
		if rows.Scan(&id, &timestamp, &app, &window, &ocr) == nil {
			out = append(out, map[string]any{"id": id, "timestamp": timestamp, "app_name": app, "window_title": window, "ocr_text": ocr})
		}
	}
	jsonOut(w, out)
}

func (h Handler) DailySummaries(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	limit, offset := page(r, 30, 100)
	rows, err := h.DB.QueryContext(r.Context(), `SELECT external_id,summary_date,visibility,payload,created_at,updated_at FROM daily_summaries WHERE user_external_uid=? ORDER BY summary_date DESC LIMIT ? OFFSET ?`, uid, limit, offset)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, date, visibility string
		var payload []byte
		var created, updated time.Time
		var value any
		if rows.Scan(&id, &date, &visibility, &payload, &created, &updated) == nil {
			_ = json.Unmarshal(payload, &value)
			out = append(out, map[string]any{"id": id, "summary_date": date, "visibility": visibility, "payload": value, "created_at": created, "updated_at": updated})
		}
	}
	jsonOut(w, out)
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func nullTime(v sql.NullTime) any {
	if !v.Valid {
		return nil
	}
	return v.Time
}
func nullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func (h Handler) Profile(w http.ResponseWriter, r *http.Request) {
	uid, err := h.uid(r)
	if err != nil {
		fail(w, 401, err)
		return
	}
	var name, email string
	var profile []byte
	err = h.DB.QueryRowContext(r.Context(), `SELECT name,email,ai_profile FROM users WHERE external_uid=?`, uid).Scan(&name, &email, &profile)
	if err != nil {
		fail(w, 404, err)
		return
	}
	var p map[string]any
	_ = json.Unmarshal(profile, &p)
	jsonOut(w, map[string]any{"name": name, "email": email, "profile_text": p["profile_text"], "generated_at": p["generated_at"], "data_sources_used": p["data_sources_used"]})
}

func (h Handler) Stream(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", "POST, HEAD, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodHead {
		if _, err := h.uid(r); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodDelete {
		if _, err := h.uid(r); err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
			fail(w, http.StatusUnauthorized, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	uid, err := h.uid(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp"`)
		fail(w, http.StatusUnauthorized, err)
		return
	}
	var msg struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&msg) != nil {
		fail(w, 400, errors.New("invalid JSON-RPC request"))
		return
	}
	result := map[string]any{}
	switch msg.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "omi-mcp-server", "version": "1.0.0"}}
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
		return
	case "tools/list":
		result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		if msg.Params.Name == "" {
			writeRPCError(w, msg.ID, -32602, "Tool name is required")
			return
		}
		required := toolScope(msg.Params.Name)
		if _, scopeErr := h.auth(r, required); scopeErr != nil {
			writeRPCError(w, msg.ID, -32003, scopeErr.Error())
			return
		}
		result = h.callTool(r, uid, msg.Params.Name, msg.Params.Arguments)
	default:
		writeRPCError(w, msg.ID, -32601, "Method not found: "+msg.Method)
		return
	}
	response := map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result}
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", mustJSON(response))
		return
	}
	jsonOut(w, response)
}

func toolScope(name string) string {
	if strings.HasSuffix(name, "create") || strings.HasSuffix(name, "update") || strings.HasSuffix(name, "delete") {
		return "action_items:write"
	}
	if strings.HasPrefix(name, "memories") {
		if strings.Contains(name, "create") || strings.Contains(name, "update") || strings.Contains(name, "delete") {
			return "memories:write"
		}
		return "memories:read"
	}
	if strings.HasPrefix(name, "action_items") {
		return "action_items:read"
	}
	if strings.HasPrefix(name, "conversations") {
		return "conversations:read"
	}
	if strings.HasPrefix(name, "goals") {
		return "goals:read"
	}
	if strings.HasPrefix(name, "chat") {
		return "chat:read"
	}
	if strings.HasPrefix(name, "people") {
		return "people:read"
	}
	if strings.HasPrefix(name, "screen") {
		return "screen_activity:read"
	}
	return "conversations:read"
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{"name": "memories_list", "description": "List user memories", "inputSchema": map[string]any{"type": "object"}},
		{"name": "memories_search", "description": "Search user memories", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]string{"type": "string"}}}},
		{"name": "conversations_list", "description": "List completed conversations", "inputSchema": map[string]any{"type": "object"}},
		{"name": "action_items_list", "description": "List action items", "inputSchema": map[string]any{"type": "object"}},
		{"name": "action_items_create", "description": "Create an action item", "inputSchema": map[string]any{"type": "object", "required": []string{"description"}}},
		{"name": "goals_list", "description": "List goals", "inputSchema": map[string]any{"type": "object"}},
		{"name": "chat_list", "description": "List chat messages", "inputSchema": map[string]any{"type": "object"}},
		{"name": "people_list", "description": "List people", "inputSchema": map[string]any{"type": "object"}},
	}
}

func (h Handler) callTool(original *http.Request, uid, name string, args map[string]any) map[string]any {
	path, method := "", http.MethodGet
	switch name {
	case "memories_list":
		path = "/v1/mcp/memories"
	case "memories_search":
		path = "/v1/mcp/memories/search"
	case "conversations_list":
		path = "/v1/mcp/conversations"
	case "action_items_list":
		path = "/v1/mcp/action-items"
	case "action_items_create":
		path = "/v1/mcp/action-items"
		method = http.MethodPost
	case "goals_list":
		path = "/v1/mcp/goals"
	case "chat_list":
		path = "/v1/mcp/chat"
	case "people_list":
		path = "/v1/mcp/people"
	default:
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": "Unknown tool"}}}
	}
	var body io.Reader
	if method == http.MethodPost {
		b, _ := json.Marshal(args)
		body = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Authorization", original.Header.Get("Authorization"))
	if method == http.MethodGet && len(args) > 0 {
		q := req.URL.Query()
		for k, v := range args {
			q.Set(k, fmt.Sprint(v))
		}
		req.URL.RawQuery = q.Encode()
	}
	if path == "/v1/mcp/memories/search" && args["query"] != nil {
		q := req.URL.Query()
		q.Set("query", fmt.Sprint(args["query"]))
		req.URL.RawQuery = q.Encode()
	}
	rr := httptest.NewRecorder()
	switch path {
	case "/v1/mcp/memories", "/v1/mcp/memories/search":
		h.Memories(rr, req)
	case "/v1/mcp/conversations":
		h.Conversations(rr, req)
	case "/v1/mcp/action-items":
		h.ActionItems(rr, req)
	case "/v1/mcp/goals":
		h.Goals(rr, req)
	case "/v1/mcp/chat":
		h.Chat(rr, req)
	case "/v1/mcp/people":
		h.People(rr, req)
	}
	var value any
	_ = json.Unmarshal(rr.Body.Bytes(), &value)
	if rr.Code >= 400 {
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": strings.TrimSpace(rr.Body.String())}}}
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(mustJSON(value))}}, "structuredContent": value}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func writeRPCError(w http.ResponseWriter, id any, code int, message string) {
	jsonOut(w, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}
