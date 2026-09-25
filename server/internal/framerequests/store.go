package framerequests

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
)

type Store interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, error)
	Delete(context.Context, string, string) error
	Copy(context.Context, string, string, string) error
}

type LocalStore struct{ Root string }

func (s LocalStore) path(uid, id string) (string, error) {
	if uid == "" || id == "" || strings.ContainsAny(uid+id, `/\\`) || strings.HasPrefix(id, ".") {
		return "", errors.New("invalid frame storage path")
	}
	return filepath.Join(s.Root, uid, id+".jpg"), nil
}
func (s LocalStore) Put(_ context.Context, uid, id string, data []byte) error {
	p, e := s.path(uid, id)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	tmp := p + ".tmp"
	if e = os.WriteFile(tmp, data, 0600); e != nil {
		return e
	}
	if e = os.Rename(tmp, p); e != nil {
		_ = os.Remove(tmp)
	}
	return e
}
func (s LocalStore) Get(_ context.Context, uid, id string) ([]byte, error) {
	p, e := s.path(uid, id)
	if e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (s LocalStore) Delete(_ context.Context, uid, id string) error {
	p, e := s.path(uid, id)
	if e != nil {
		return e
	}
	e = os.Remove(p)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (s LocalStore) Copy(ctx context.Context, uid, from, to string) error {
	data, e := s.Get(ctx, uid, from)
	if e != nil {
		return e
	}
	return s.Put(ctx, uid, to, data)
}

type GCSStore struct {
	Client *storage.Client
	Bucket string
}

func NewGCSStore(ctx context.Context, bucket string) (*GCSStore, error) {
	if strings.TrimSpace(bucket) == "" {
		return nil, nil
	}
	client, e := storage.NewClient(ctx)
	if e != nil {
		return nil, e
	}
	return &GCSStore{Client: client, Bucket: bucket}, nil
}
func (s *GCSStore) object(uid, id string) (*storage.ObjectHandle, error) {
	if s == nil || s.Client == nil || s.Bucket == "" {
		return nil, errors.New("frame GCS store is not configured")
	}
	if uid == "" || id == "" || strings.ContainsAny(uid+id, `/\\`) || strings.HasPrefix(id, ".") {
		return nil, errors.New("invalid frame storage id")
	}
	return s.Client.Bucket(s.Bucket).Object(filepath.ToSlash(filepath.Join(uid, id+".jpg"))), nil
}
func (s *GCSStore) Put(ctx context.Context, uid, id string, data []byte) error {
	o, e := s.object(uid, id)
	if e != nil {
		return e
	}
	w := o.NewWriter(ctx)
	w.ContentType = "image/jpeg"
	w.CacheControl = "private, no-cache"
	if _, e = w.Write(data); e != nil {
		_ = w.Close()
		return e
	}
	return w.Close()
}
func (s *GCSStore) Get(ctx context.Context, uid, id string) ([]byte, error) {
	o, e := s.object(uid, id)
	if e != nil {
		return nil, e
	}
	r, e := o.NewReader(ctx)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return io.ReadAll(r)
}
func (s *GCSStore) Delete(ctx context.Context, uid, id string) error {
	o, e := s.object(uid, id)
	if e != nil {
		return e
	}
	e = o.Delete(ctx)
	if e == storage.ErrObjectNotExist {
		return nil
	}
	return e
}
func (s *GCSStore) Copy(ctx context.Context, uid, from, to string) error {
	src, e := s.object(uid, from)
	if e != nil {
		return e
	}
	dst, e := s.object(uid, to)
	if e != nil {
		return e
	}
	_, e = dst.CopierFrom(src).Run(ctx)
	return e
}
func (s *GCSStore) Close() error {
	if s == nil || s.Client == nil {
		return nil
	}
	return s.Client.Close()
}
