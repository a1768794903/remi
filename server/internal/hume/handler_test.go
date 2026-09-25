package hume

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCallbackDefensivelyHandlesMalformedNestedProsody(t *testing.T) {
	callback, err := ParseCallback([]byte(`{"job_id":"job-1","status":"COMPLETED","predictions":[{"results":{"predictions":[{"models":{"prosody":{"grouped_predictions":[{"predictions":[{"time":{"begin":"bad","end":2},"emotions":[{"name":"joy","score":"bad"},{"name":"calm","score":0.9}]}]}]}}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if callback.JobID != "job-1" || len(callback.Predictions) != 1 || callback.Predictions[0].Time.Begin != 0 || callback.Predictions[0].Emotions[0].Score != 0 {
		t.Fatalf("unexpected callback: %+v", callback)
	}
	if got := TopEmotionNames([]Emotion{{Name: "joy", Score: 0.9}, {Name: "calm", Score: 0.8}}, 1, 0.5); len(got) != 1 || got[0] != "joy" {
		t.Fatalf("top emotions=%v", got)
	}
}

func TestCallbackRejectsMissingJobID(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v1/agents/hume/callback", strings.NewReader(`{"status":"COMPLETED"}`))
	rec := httptest.NewRecorder()
	h.Callback(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
