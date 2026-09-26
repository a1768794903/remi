package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"remi/server/internal/auth"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCostUsesProviderModalityRates(t *testing.T) {
	got := cost("openai", "gpt-realtime-2", usage{InputText: 1_000_000, CachedText: 100_000, InputAudio: 1_000_000, OutputText: 1_000_000, OutputAudio: 1_000_000})
	const want = 3_600_000 + 40_000 + 32_000_000 + 24_000_000 + 64_000_000
	if got != want {
		t.Fatalf("cost=%d want=%d", got, want)
	}
}

func TestHandlerReturnsStructuredErrorForBadProvider(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v2/realtime/session", strings.NewReader(`{"provider":"other"}`))
	res := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(h.Mint)).ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"reason":"bad_provider"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestHandlerRejectsUnconfiguredProvider(t *testing.T) {
	h := Handler{}
	req := httptest.NewRequest(http.MethodPost, "/v2/realtime/session", strings.NewReader(`{"provider":"openai"}`))
	res := httptest.NewRecorder()
	auth.Middleware("dev")(http.HandlerFunc(h.Mint)).ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), `"reason":"provider_not_configured"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestRecordUsageWritesRealtimeAndLLMUsageLedger(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO realtime_usage").
		WithArgs("uid-1", "turn-1", "openai", openAIModel, int64(12), int64(7), int64(3), int64(1234)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO llm_usage").
		WithArgs("uid-1", int64(12), int64(7), int64(1234)).
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()

	inserted, err := recordUsage(context.Background(), db, "uid-1", usageRequest{
		Provider: "openai", Model: "attacker-selected-model", TurnID: "turn-1",
		InputText: 12, OutputText: 7, Cached: 3,
	}, usage{InputText: 12, OutputText: 7, CachedText: 3}, 26, 1234)
	if err != nil || !inserted {
		t.Fatalf("recordUsage() = inserted=%v err=%v", inserted, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordUsageDuplicateTurnDoesNotWriteLLMUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO realtime_usage").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	inserted, err := recordUsage(context.Background(), db, "uid-1", usageRequest{Provider: "openai", TurnID: "turn-1"}, usage{}, 1, 1234)
	if err != nil || inserted {
		t.Fatalf("recordUsage() = inserted=%v err=%v", inserted, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLQuotaEnforcerReturnsPaymentRequiredAtBasicLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT plan,status FROM subscriptions").
		WithArgs("uid-1").
		WillReturnRows(sqlmock.NewRows([]string{"plan", "status"}).AddRow("basic", "active"))
	mock.ExpectQuery("SELECT COALESCE\\(SUM\\(questions\\)").
		WithArgs("uid-1").
		WillReturnRows(sqlmock.NewRows([]string{"questions", "cost_micro_usd"}).AddRow(30, 0))

	err = SQLQuotaEnforcer(db)(context.Background(), "uid-1")
	quota, ok := err.(quotaError)
	if !ok || quota.Status != http.StatusPaymentRequired || quota.Body["unit"] != "questions" {
		t.Fatalf("quota error = %#v, want HTTP 402 questions", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
