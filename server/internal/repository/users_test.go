package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnsureUserUsesExternalUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := sqlmock.AnyArg()
	mock.ExpectExec("INSERT INTO users").
		WithArgs("firebase-user-1", "user@example.com", "Alice", now, now).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, external_uid").
		WithArgs("firebase-user-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "external_uid", "email", "name"}).AddRow(1, "firebase-user-1", "user@example.com", "Alice"))

	user, err := (Users{DB: db}).Ensure(context.Background(), "firebase-user-1", "user@example.com", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if user.ExternalUID != "firebase-user-1" || user.ID != 1 {
		t.Fatalf("unexpected user: %+v", user)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
