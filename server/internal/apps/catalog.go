package apps

import (
	"encoding/json"
	"net/http"
)

type SelectOption struct {
	Title string `json:"title"`
	ID    string `json:"id"`
}

type Trigger struct {
	Title string `json:"title"`
	ID    string `json:"id"`
}

type Action struct {
	Title       string `json:"title"`
	ID          string `json:"id"`
	DocURL      string `json:"doc_url,omitempty"`
	Description string `json:"description,omitempty"`
}

type Capability struct {
	Title    string         `json:"title"`
	ID       string         `json:"id"`
	Triggers []Trigger      `json:"triggers,omitempty"`
	Actions  []Action       `json:"actions,omitempty"`
	Scopes   []SelectOption `json:"scopes,omitempty"`
}

func Categories() []SelectOption {
	return []SelectOption{
		{Title: "Conversation Analysis", ID: "conversation-analysis"},
		{Title: "Personality Clone", ID: "personality-emulation"},
		{Title: "Health", ID: "health-and-wellness"},
		{Title: "Education", ID: "education-and-learning"},
		{Title: "Communication", ID: "communication-improvement"},
		{Title: "Emotional Support", ID: "emotional-and-mental-support"},
		{Title: "Productivity", ID: "productivity-and-organization"},
		{Title: "Entertainment", ID: "entertainment-and-fun"},
		{Title: "Financial", ID: "financial"},
		{Title: "Travel", ID: "travel-and-exploration"},
		{Title: "Safety", ID: "safety-and-security"},
		{Title: "Shopping", ID: "shopping-and-commerce"},
		{Title: "Social", ID: "social-and-relationships"},
		{Title: "News", ID: "news-and-information"},
		{Title: "Utilities", ID: "utilities-and-tools"},
		{Title: "Other", ID: "other"},
	}
}

func Capabilities() []Capability {
	doc := "https://docs.omi.me/doc/developer/apps/Import"
	return []Capability{
		{Title: "Chat", ID: "chat"},
		{Title: "Conversations", ID: "memories"},
		{Title: "External Integration", ID: "external_integration",
			Triggers: []Trigger{{Title: "Audio Bytes", ID: "audio_bytes"}, {Title: "Conversation Creation", ID: "memory_creation"}, {Title: "Transcript Processed", ID: "transcript_processed"}},
			Actions: []Action{
				{Title: "Create conversations", ID: "create_conversation", DocURL: doc, Description: "Extend user conversations by making a POST request to the OMI System."},
				{Title: "Create memories", ID: "create_facts", DocURL: doc, Description: "Create new memories for the user through the OMI System."},
				{Title: "Read conversations", ID: "read_conversations", DocURL: doc, Description: "Access and read all user conversation history through the OMI System."},
				{Title: "Read memories", ID: "read_memories", DocURL: doc, Description: "Access and read all user memories through the OMI System."},
				{Title: "Read tasks", ID: "read_tasks", DocURL: doc, Description: "Access and read all user tasks through the OMI System."},
			}},
		{Title: "Notification", ID: "proactive_notification", Scopes: []SelectOption{{Title: "User Name", ID: "user_name"}, {Title: "User Facts", ID: "user_facts"}, {Title: "User Conversations", ID: "user_context"}, {Title: "User Chat", ID: "user_chat"}}},
	}
}

func NotificationScopes() []SelectOption {
	return []SelectOption{{Title: "User Name", ID: "user_name"}, {Title: "User Memories", ID: "user_facts"}, {Title: "User Conversations", ID: "user_context"}, {Title: "User Chat", ID: "user_chat"}}
}

func PaymentPlans() []SelectOption {
	return []SelectOption{{Title: "Monthly Recurring", ID: "monthly_recurring"}}
}

func HandlerCategories(w http.ResponseWriter, _ *http.Request)   { _ = writeJSON(w, Categories()) }
func HandlerCapabilities(w http.ResponseWriter, _ *http.Request) { _ = writeJSON(w, Capabilities()) }
func HandlerNotificationScopes(w http.ResponseWriter, _ *http.Request) {
	_ = writeJSON(w, NotificationScopes())
}
func HandlerPaymentPlans(w http.ResponseWriter, _ *http.Request) { _ = writeJSON(w, PaymentPlans()) }

func writeJSON(w http.ResponseWriter, value any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(value)
}
