package hume

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

type Emotion struct {
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

type Interval struct {
	Begin float64 `json:"begin"`
	End   float64 `json:"end"`
}

type Prediction struct {
	Time     Interval  `json:"time"`
	Emotions []Emotion `json:"emotions"`
}

type Callback struct {
	JobID       string       `json:"job_id"`
	Status      string       `json:"status"`
	Predictions []Prediction `json:"predictions"`
}

type PredictionRow struct {
	JobID        string
	Sequence     int
	BeginSeconds float64
	EndSeconds   float64
	EmotionsJSON []byte
}

func FlattenPredictions(callback Callback) ([]PredictionRow, error) {
	rows := make([]PredictionRow, 0, len(callback.Predictions))
	for sequence, prediction := range callback.Predictions {
		emotions, err := json.Marshal(prediction.Emotions)
		if err != nil {
			return nil, err
		}
		rows = append(rows, PredictionRow{JobID: callback.JobID, Sequence: sequence, BeginSeconds: prediction.Time.Begin, EndSeconds: prediction.Time.End, EmotionsJSON: emotions})
	}
	return rows, nil
}

func number(v any) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return 0
}

func ParseCallback(raw []byte) (Callback, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return Callback{}, err
	}
	jobID, _ := root["job_id"].(string)
	if jobID == "" {
		return Callback{}, errors.New("job_id is required")
	}
	status, _ := root["status"].(string)
	out := Callback{JobID: jobID, Status: status, Predictions: []Prediction{}}
	predictions, _ := root["predictions"].([]any)
	if len(predictions) == 0 {
		return out, nil
	}
	first, _ := predictions[0].(map[string]any)
	results, _ := first["results"].(map[string]any)
	outer, _ := results["predictions"].([]any)
	for _, rawPrediction := range outer {
		predictionMap, _ := rawPrediction.(map[string]any)
		models, _ := predictionMap["models"].(map[string]any)
		prosody, _ := models["prosody"].(map[string]any)
		groups, _ := prosody["grouped_predictions"].([]any)
		for _, rawGroup := range groups {
			group, _ := rawGroup.(map[string]any)
			items, _ := group["predictions"].([]any)
			for _, rawItem := range items {
				item, _ := rawItem.(map[string]any)
				interval, _ := item["time"].(map[string]any)
				p := Prediction{Time: Interval{Begin: number(interval["begin"]), End: number(interval["end"])}, Emotions: []Emotion{}}
				emotions, _ := item["emotions"].([]any)
				for _, rawEmotion := range emotions {
					emotion, _ := rawEmotion.(map[string]any)
					name, _ := emotion["name"].(string)
					p.Emotions = append(p.Emotions, Emotion{Name: name, Score: number(emotion["score"])})
				}
				out.Predictions = append(out.Predictions, p)
			}
		}
	}
	return out, nil
}

func TopEmotionNames(emotions []Emotion, k int, threshold float64) []string {
	sums := map[string]float64{}
	for _, emotion := range emotions {
		sums[emotion.Name] += emotion.Score
	}
	count := float64(len(sums))
	if count == 0 || k <= 0 {
		return []string{}
	}
	type ranked struct {
		name  string
		score float64
	}
	rankedItems := make([]ranked, 0, len(sums))
	for name, score := range sums {
		if score >= threshold {
			rankedItems = append(rankedItems, ranked{name, score / count})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool { return rankedItems[i].score > rankedItems[j].score })
	if k > len(rankedItems) {
		k = len(rankedItems)
	}
	out := make([]string, 0, k)
	for _, item := range rankedItems[:k] {
		out = append(out, item.name)
	}
	return out
}

type Handler struct{ DB *sql.DB }

func (h Handler) Callback(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	var body any
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "invalid Hume callback", http.StatusBadRequest)
		return
	}
	encoded, _ := json.Marshal(body)
	callback, err := ParseCallback(encoded)
	if err != nil {
		http.Error(w, "Job callback is invalid", http.StatusBadRequest)
		return
	}
	if h.DB == nil {
		http.Error(w, "Hume callback storage is not configured", http.StatusServiceUnavailable)
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
		return
	}
	rollback := func() {
		_ = tx.Rollback()
	}
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO hume_callbacks(job_id,status,payload,created_at,updated_at) VALUES(?,?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE status=VALUES(status),payload=VALUES(payload),updated_at=UTC_TIMESTAMP(6)`, callback.JobID, callback.Status, encoded); err != nil {
		rollback()
		http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := FlattenPredictions(callback)
	if err != nil {
		rollback()
		http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM hume_emotion_predictions WHERE job_id=?`, callback.JobID); err != nil {
		rollback()
		http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
		return
	}
	for _, row := range rows {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO hume_emotion_predictions(job_id,sequence,begin_seconds,end_seconds,emotions,created_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6))`, row.JobID, row.Sequence, row.BeginSeconds, row.EndSeconds, row.EmotionsJSON); err != nil {
			rollback()
			http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, "Hume callback storage unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{})
}
