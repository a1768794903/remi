package knowledgegraph

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
)

type Handler struct {
	DB       *sql.DB
	Provider chat.Provider
}
type Graph struct {
	Nodes []map[string]any `json:"nodes"`
	Edges []map[string]any `json:"edges"`
}

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (h Handler) uid(r *http.Request) (string, error) { return auth.UserID(r.Context()) }
func (h Handler) read(r *http.Request, uid string) (Graph, error) {
	var raw string
	e := h.DB.QueryRowContext(r.Context(), `SELECT graph_json FROM knowledge_graphs WHERE user_external_uid=?`, uid).Scan(&raw)
	if e == sql.ErrNoRows {
		return Graph{Nodes: []map[string]any{}, Edges: []map[string]any{}}, nil
	}
	if e != nil {
		return Graph{}, e
	}
	var g Graph
	if e = json.Unmarshal([]byte(raw), &g); e != nil {
		return Graph{}, e
	}
	return g, nil
}
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	g, e := h.read(r, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	limit := 500
	if len(g.Nodes) > limit {
		g.Nodes = g.Nodes[:limit]
	}
	if len(g.Edges) > limit {
		g.Edges = g.Edges[:limit]
	}
	write(w, 200, map[string]any{"nodes": g.Nodes, "edges": g.Edges, "truncated": len(g.Nodes) >= limit || len(g.Edges) >= limit, "node_count": len(g.Nodes), "edge_count": len(g.Edges), "node_limit": limit, "edge_limit": limit})
}
func (h Handler) Canonical(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	g, e := h.read(r, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	limit := 100
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 500 {
		limit = n
	}
	offset := 0
	if c := r.URL.Query().Get("cursor"); c != "" {
		b, er := base64.RawURLEncoding.DecodeString(c)
		if er != nil {
			http.Error(w, "invalid_or_stale_cursor", 400)
			return
		}
		offset, _ = strconv.Atoi(string(b))
		if offset < 0 || offset > len(g.Nodes) {
			http.Error(w, "invalid_or_stale_cursor", 400)
			return
		}
	}
	end := offset + limit
	if end > len(g.Nodes) {
		end = len(g.Nodes)
	}
	nodes := g.Nodes[offset:end]
	more := end < len(g.Nodes)
	var next any
	if more {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	write(w, 200, map[string]any{"nodes": nodes, "edges": g.Edges, "has_more": more, "next_cursor": next, "catalog_nodes": []map[string]any{}})
}
func (h Handler) Extract(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	var in struct {
		Text            string `json:"text"`
		UserName        string `json:"user_name"`
		IncludeExisting bool   `json:"include_existing"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || len(strings.TrimSpace(in.Text)) == 0 || len(in.Text) > 100000 {
		http.Error(w, "invalid text", 400)
		return
	}
	g, e := h.extract(r.Context(), u, in.Text, in.UserName)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	write(w, 200, g)
}
func (h Handler) extract(ctx context.Context, uid, text, name string) (Graph, error) {
	if h.Provider == nil {
		return Graph{}, errors.New("knowledge graph provider is not configured")
	}
	prompt := `Extract a knowledge graph from the supplied text. Return ONLY strict JSON with this shape: {"nodes":[{"id":"stable-slug","label":"...","node_type":"concept","aliases":[],"memory_ids":[]}],"edges":[{"id":"stable-edge","source_id":"...","target_id":"...","label":"...","memory_ids":[]}]}. Do not invent facts. User: ` + name + "\nText:\n" + text
	answer, e := h.Provider.Complete(ctx, []chat.Turn{{Role: "system", Content: prompt}})
	if e != nil {
		return Graph{}, e
	}
	start := strings.Index(answer, "{")
	end := strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return Graph{}, errors.New("knowledge graph provider returned invalid JSON")
	}
	var g Graph
	if e = json.Unmarshal([]byte(answer[start:end+1]), &g); e != nil {
		return Graph{}, e
	}
	return g, nil
}
func (h Handler) Rebuild(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	rows, e := h.DB.QueryContext(r.Context(), `SELECT m.id,m.content FROM memories m JOIN users u ON u.id=m.user_id WHERE u.external_uid=? AND m.is_dismissed=0 ORDER BY m.created_at DESC LIMIT 500`, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var id int64
		var content string
		if rows.Scan(&id, &content) == nil {
			parts = append(parts, "memory "+strconv.FormatInt(id, 10)+": "+content)
		}
	}
	g, e := h.extract(r.Context(), u, strings.Join(parts, "\n"), "")
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	raw, _ := json.Marshal(g)
	_, e = h.DB.ExecContext(r.Context(), `INSERT INTO knowledge_graphs(user_external_uid,graph_json,updated_at) VALUES(?,?,?) ON DUPLICATE KEY UPDATE graph_json=VALUES(graph_json),updated_at=VALUES(updated_at)`, u, raw, time.Now().UTC())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	write(w, 200, map[string]any{"status": "rebuilt", "nodes_count": len(g.Nodes), "edges_count": len(g.Edges)})
}
func (h Handler) Delete(w http.ResponseWriter, r *http.Request) {
	u, e := h.uid(r)
	if e != nil {
		http.Error(w, e.Error(), 401)
		return
	}
	_, e = h.DB.ExecContext(r.Context(), `DELETE FROM knowledge_graphs WHERE user_external_uid=?`, u)
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	write(w, 200, map[string]string{"status": "deleted"})
}
