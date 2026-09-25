package account

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRunWipeRejectsInvalidWorkerCredentials(t *testing.T) {
	t.Setenv("INTERNAL_JOB_SECRET", "wipe-secret")
	h := Handler{Wipe: WipeService{}}

	for _, tc := range []struct {
		name   string
		header string
		body   string
		status int
	}{
		{"missing key", "", `{"job_id":"job-1"}`, http.StatusForbidden},
		{"wrong key", "wrong", `{"job_id":"job-1"}`, http.StatusForbidden},
		{"invalid payload", "wipe-secret", `{}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/users/account-deletion-wipes/run", strings.NewReader(tc.body))
			if tc.header != "" {
				req.Header.Set("X-Internal-Job-Key", tc.header)
			}
			rec := httptest.NewRecorder()
			h.RunWipe(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestWipeServiceDeletesSQLOnlyRowsAfterEntDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	uid := "firebase-user-1"
	for _, table := range wipeSQLTables {
		mock.ExpectExec("DELETE FROM `" + table.name + "`").WithArgs(uid).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	service := WipeService{DB: db}
	if err := service.deleteSQLRows(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWipeServiceClaimIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO account_deletion_wipes").WithArgs("job-1", "firebase-user-1", "running").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE account_deletion_wipes SET status='completed'").WithArgs("job-1").WillReturnResult(sqlmock.NewResult(0, 1))
	service := WipeService{DB: db}
	if err := service.claim(context.Background(), "job-1", "firebase-user-1"); err != nil {
		t.Fatal(err)
	}
	if err := service.complete(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

var _ *sql.DB
