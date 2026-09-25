package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID          int64
	ExternalUID string
	Email       string
	Name        string
}

type Users struct{ DB *sql.DB }

// Ensure mirrors the Python user bootstrap behavior: authentication identifies
// a Firebase UID first, while the relational integer ID stays an internal key.
func (r Users) Ensure(ctx context.Context, externalUID, email, name string) (User, error) {
	if r.DB == nil {
		return User{}, errors.New("user repository is not configured")
	}
	if email == "" {
		email = externalUID
	}
	now := time.Now().UTC()
	_, err := r.DB.ExecContext(ctx, "INSERT INTO users (external_uid, email, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE email = VALUES(email), name = VALUES(name), updated_at = VALUES(updated_at)", externalUID, email, name, now, now)
	if err != nil {
		return User{}, err
	}
	return r.GetByExternalUID(ctx, externalUID)
}

func (r Users) GetByExternalUID(ctx context.Context, externalUID string) (User, error) {
	var user User
	err := r.DB.QueryRowContext(ctx, "SELECT id, external_uid, email, name FROM users WHERE external_uid = ?", externalUID).Scan(&user.ID, &user.ExternalUID, &user.Email, &user.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}
