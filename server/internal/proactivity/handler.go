package proactivity

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"remi/server/internal/auth"
	"remi/server/internal/chat"
)

type Handler struct{ Provider chat.Provider }

type request struct {
	Operation           string         `json:"operation"`
	Messages            []message      `json:"messages"`
	ResponseFormat      map[string]any `json:"response_format"`
	MaxCompletionTokens int            `json:"max_completion_tokens"`
	CacheKey            string         `json:"cache_key"`
	Metadata            map[string]any `json:"metadata"`
}
type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func (h Handler) Complete(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.UserID(r.Context()); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024+1)
	var in request
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(in.Messages) == 0 || len(in.Messages) > 16 {
		writeError(w, http.StatusBadRequest, "messages must contain between 1 and 16 items")
		return
	}
	if in.Operation != "proactive_extraction" && in.Operation != "proactive_reasoning" {
		writeError(w, http.StatusBadRequest, "unsupported operation")
		return
	}
	if in.Operation != "proactive_reasoning" && in.CacheKey != "" {
		writeError(w, http.StatusBadRequest, "explicit caching is available only for proactive_reasoning")
		return
	}
	if err := validateResponseFormat(in.ResponseFormat); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.Provider == nil {
		writeError(w, http.StatusServiceUnavailable, "Proactive model provider is not configured")
		return
	}
	turns := make([]chat.Turn, 0, len(in.Messages))
	for _, item := range in.Messages {
		content, ok := item.Content.(string)
		if !ok {
			encoded, _ := json.Marshal(item.Content)
			content = string(encoded)
		}
		turns = append(turns, chat.Turn{Role: item.Role, Content: content})
	}
	answer, err := h.Provider.Complete(r.Context(), turns)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Proactive model provider unavailable")
		return
	}
	var response map[string]any
	if json.Unmarshal([]byte(answer), &response) != nil || response == nil {
		writeError(w, http.StatusUnprocessableEntity, "Proactive model returned invalid structured output")
		return
	}
	lane := "omi:auto:desktop-proactive-extraction"
	if in.Operation == "proactive_reasoning" {
		lane = "omi:auto:desktop-proactive-reasoning"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"operation":      in.Operation,
		"lane":           lane,
		"provider_model": "configured",
		"usage":          map[string]int{"cached_tokens": 0, "cache_write_tokens": 0},
		"cache_write":    false,
		"fallback_class": "none",
		"response":       response,
	})
}

func validateResponseFormat(value map[string]any) error {
	if value == nil || value["type"] != "json_schema" {
		return errors.New("response_format.type must be json_schema")
	}
	schema, ok := value["json_schema"].(map[string]any)
	if !ok {
		return errors.New("response_format.json_schema must be an object")
	}
	if name, ok := schema["name"].(string); !ok || strings.TrimSpace(name) == "" {
		return errors.New("response_format.json_schema.name is required")
	}
	if schema["strict"] != true {
		return errors.New("response_format.json_schema.strict must be true")
	}
	if _, ok := schema["schema"].(map[string]any); !ok {
		return errors.New("response_format.json_schema.schema must be an object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
