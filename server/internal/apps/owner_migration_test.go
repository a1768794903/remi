package apps

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMigrateOwnerMovesAppsAndMemoriesAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM users WHERE external_uid=\\? FOR UPDATE").WithArgs("old").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(11))
	mock.ExpectQuery("SELECT id FROM users WHERE external_uid=\\? FOR UPDATE").WithArgs("new").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(22))
	mock.ExpectExec("UPDATE plugins_data SET uid=").WithArgs("new", "old").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE memories SET user_id=").WithArgs(int64(22), int64(11)).WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()
	if err := (Service{DB: db}).MigrateOwner(context.Background(), "new", "old"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateOwnerRejectsSameOrMissingIdentity(t *testing.T) {
	if err := (Service{}).MigrateOwner(context.Background(), "same", "same"); !errors.Is(err, ErrInvalidOwnerMigration) {
		t.Fatalf("err=%v", err)
	}
}

var _ *sql.DB
