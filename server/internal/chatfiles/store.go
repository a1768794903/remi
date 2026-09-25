package chatfiles

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"cloud.google.com/go/storage"
)

type ThumbnailStore interface {
	Put(ctx context.Context, uid, name, contentType string, data []byte) (string, error)
}

type GCSStore struct {
	Client *storage.Client
	Bucket string
}

func NewGCSStore(ctx context.Context, bucket string) (ThumbnailStore, func() error, error) {
	if strings.TrimSpace(bucket) == "" {
		return nil, func() error { return nil }, nil
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, func() error { return nil }, err
	}
	store := GCSStore{Client: client, Bucket: bucket}
	return store, client.Close, nil
}
func (s GCSStore) Put(ctx context.Context, uid, name, contentType string, data []byte) (string, error) {
	if s.Client == nil || strings.TrimSpace(s.Bucket) == "" {
		return "", errors.New("GCS thumbnail store is not configured")
	}
	object := path.Join(uid, name)
	writer := s.Client.Bucket(s.Bucket).Object(object).NewWriter(ctx)
	writer.ContentType = contentType
	writer.CacheControl = "public, no-cache"
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://storage.googleapis.com/%s/%s", s.Bucket, object), nil
}
