package trends

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
)

var categories = map[string]bool{"ceo": true, "company": true, "software_product": true, "hardware_product": true, "ai_product": true}
var validTopics = map[string]bool{"Adam Neumann": true, "Adobe": true, "Adobe Photoshop": true, "Airbnb": true, "Alexandr Wang": true, "Amazon": true, "Amazon Alexa": true, "Amazon Echo": true, "Andrew Wilson": true, "Andy Jassy": true, "Anthony Wood": true, "Anthropic Claude 2": true, "Apple": true, "Apple Vision Pro": true, "Apple Watch 9": true, "Arvind Krishna": true, "Asana": true, "AT&T": true, "Atlassian Jira": true, "Austin Russell": true, "Bob Chapek": true, "Boeing": true, "BP": true, "Brex": true, "Brian Chesky": true, "ChatGPT clones": true, "Christian Klein": true, "Clear AI": true, "Clearview AI": true, "Clubhouse": true, "Cohere": true, "Dara Khosrowshahi": true, "Darius Adamczyk": true, "Databricks": true, "Databricks AI": true, "Datadog": true, "David Zaslav": true, "Disney": true, "DJI Mavic 3": true, "Dylan Field": true, "Dyson V16": true, "Elon Musk": true, "Evernote": true, "Facebook": true, "Facebook AI": true, "Facebook Workplace": true, "FedEx": true, "Figma": true, "Fitbit Charge 6": true, "Framework Laptop": true, "Fran Horowitz": true, "Frank Slootman": true, "Frontier Airlines": true, "GameStop": true, "GitHub": true, "GitHub Copilot": true, "Google DeepMind": true, "Google Duplex": true, "Google Pixel 9": true, "Google Stadia": true, "Google Workspace": true, "Google/Alphabet": true, "Grammarly AI": true, "Henrique Dubugras": true, "Howard Schultz": true, "HubSpot": true, "Hugging Face": true, "Hugging Face Transformers": true, "IBM": true, "IBM Watson": true, "Intel AI Suite": true, "iPhone 16": true, "Jasper AI": true, "Jensen Huang": true, "Juul": true, "Kaspersky": true, "Kazuhiro Tsuga": true, "Lisa Su": true, "Logitech G Pro X": true, "Luminar": true, "Luminar LiDAR": true, "Lumix S5 II": true, "MacBook Pro": true, "Marc Benioff": true, "Mark Zuckerberg": true, "Mary Barra": true, "Meta": true, "Meta AI": true, "Meta Horizon AI": true, "Meta Horizon Worlds": true, "Microsoft": true, "Microsoft Copilot": true, "Microsoft Surface Pro 10": true, "Microsoft Tay": true, "Microsoft Teams": true, "MidJourney": true, "Monday.com": true, "Nestlé": true, "Notion": true, "Notion AI": true, "Nvidia": true, "Nvidia Omniverse": true, "Nvidia RTX 5090": true, "Oculus Quest 4": true, "OpenAI": true, "OpenAI GPT-4": true, "OpenAI GPT-5": true, "Oracle": true, "Palantir": true, "Palantir Foundry": true, "Parag Agrawal": true, "Patrick Collison": true, "Peloton": true, "Quibi": true, "Raj Subramaniam": true, "Replika": true, "Ring AI": true, "Robinhood": true, "Robinhood AI Trading": true, "Roland Busch": true, "Runway Gen-2": true, "Ryan Breslow": true, "Salesforce": true, "Salesforce AI": true, "Salesforce CRM": true, "Salesforce Einstein": true, "Salesforce Marketing Cloud": true, "Sam Altman": true, "Samsung Bixby": true, "Samsung Galaxy Fold 4": true, "SAP S/4HANA": true, "Satya Nadella": true, "Scale AI": true, "ServiceNow": true, "Shantanu Narayen": true, "Shopify": true, "Slack": true, "Slack Threads": true, "Snapchat": true, "Snowflake": true, "Sony PlayStation 6": true, "SpaceX": true, "SpaceX Starship": true, "Square": true, "Stéphane Bancel": true, "Stripe": true, "Sundar Pichai": true, "Synthesia": true, "Tableau": true, "Tesla": true, "Tesla Cybertruck": true, "Tesla FSD": true, "TikTok": true, "TikTok AI": true, "TikTok’s Creator Tools": true, "Tim Cook": true, "Tinder AI": true, "Trello": true, "Twitter AI moderation": true, "Uber": true, "Uber AI": true, "Uber Driver App": true, "Vlad Tenev": true, "Warner Bros. Discovery": true, "Watson Health": true, "WeWork": true, "William McDermott": true, "X (formerly Twitter)": true, "Yuanqing Yang": true, "Zoom": true, "Zoom AI transcription": true, "ZoomInfo": true}

type topic struct {
	ID, Topic string
	MemoryIDs []string
}
type cleanTopic struct {
	ID            string `json:"id"`
	Topic         string `json:"topic"`
	MemoriesCount int    `json:"memories_count"`
}
type category struct {
	ID       string       `json:"id"`
	Category string       `json:"category"`
	Type     string       `json:"type"`
	Topics   []cleanTopic `json:"topics"`
}
type Service struct{ DB *sql.DB }

func cleanTopics(items []topic) []cleanTopic {
	sort.SliceStable(items, func(i, j int) bool { return len(items[i].MemoryIDs) > len(items[j].MemoryIDs) })
	out := make([]cleanTopic, 0, len(items))
	for _, item := range items {
		if !validTopics[item.Topic] {
			continue
		}
		out = append(out, cleanTopic{ID: item.ID, Topic: item.Topic, MemoriesCount: len(item.MemoryIDs)})
	}
	return out
}

func (s Service) List(ctx context.Context) ([]category, error) {
	if s.DB == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT c.id,c.category,c.type,t.id,t.topic,t.memory_ids FROM trend_categories c LEFT JOIN trend_topics t ON t.category_id=c.id ORDER BY c.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]*category{}
	order := []string{}
	for rows.Next() {
		var cid, cat, typ, tid, topic string
		var raw []byte
		if err := rows.Scan(&cid, &cat, &typ, &tid, &topic, &raw); err != nil {
			return nil, err
		}
		if !categories[cat] {
			continue
		}
		item, ok := byID[cid]
		if !ok {
			item = &category{ID: cid, Category: cat, Type: typ}
			byID[cid] = item
			order = append(order, cid)
		}
		if tid != "" && validTopics[topic] {
			var ids []string
			_ = json.Unmarshal(raw, &ids)
			item.Topics = append(item.Topics, cleanTopic{ID: tid, Topic: topic, MemoriesCount: len(ids)})
		}
	}
	out := make([]category, 0, len(order))
	for _, id := range order {
		item := *byID[id]
		sort.SliceStable(item.Topics, func(i, j int) bool { return item.Topics[i].MemoriesCount > item.Topics[j].MemoriesCount })
		out = append(out, item)
	}
	return out, rows.Err()
}

type Handler struct{ Service Service }

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.List(r.Context())
	if err != nil {
		http.Error(w, "trends unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}
