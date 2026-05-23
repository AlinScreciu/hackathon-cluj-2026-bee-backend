// Package storage provides an audio-blob store abstraction with a local
// filesystem implementation (dev) and a Cloudflare R2 implementation
// (deploy). Used by the ElevenLabs client to cache generated MP3s under a
// content-addressed key so identical text is only ever paid for once.
//
// SignedURL: for R2 this issues a short-lived S3 presigned GET so the
// bucket stays private (GDPR Art. 32 — the audio contains a beekeeper's
// first name + apiary name, which is indirectly identifying via the
// public ANSVSA apiary registry). For Local, signing is a no-op because
// the file server is unauthenticated dev infrastructure.
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Storage interface {
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	SignedPutURL(ctx context.Context, key string, ttl time.Duration, contentType string) (string, error)
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, data []byte, contentType string) error
}

// --- Local filesystem ---

type Local struct {
	rootDir    string // disk root, e.g. "uploads"
	baseURL    string // public base URL, e.g. APP_BASE_URL
	pathPrefix string // URL path prefix served by the static file handler
}

func NewLocal(rootDir, baseURL, pathPrefix string) *Local {
	return &Local{
		rootDir:    rootDir,
		baseURL:    strings.TrimRight(baseURL, "/"),
		pathPrefix: "/" + strings.Trim(pathPrefix, "/"),
	}
}

func (l *Local) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return l.baseURL + l.pathPrefix + "/" + key, nil
}

// SignedPutURL returns a URL the client can PUT bytes to. In dev this points
// at a raw chi handler that writes to disk; production should always go via R2.
func (l *Local) SignedPutURL(_ context.Context, key string, _ time.Duration, _ string) (string, error) {
	return l.baseURL + "/api/v1/uploads/raw/" + key, nil
}

func (l *Local) Exists(_ context.Context, key string) (bool, error) {
	_, err := os.Stat(filepath.Join(l.rootDir, key))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("storage local: stat: %w", err)
}

func (l *Local) Put(_ context.Context, key string, data []byte, _ string) error {
	p := filepath.Join(l.rootDir, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("storage local: mkdir: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("storage local: write: %w", err)
	}
	return nil
}

// --- Cloudflare R2 ---

type R2Config struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
}

type R2 struct {
	client *minio.Client
	bucket string
}

func NewR2(cfg R2Config) (*R2, error) {
	if cfg.AccountID == "" || cfg.Bucket == "" {
		return nil, errors.New("storage r2: account_id and bucket are required")
	}
	endpoint := fmt.Sprintf("%s.r2.cloudflarestorage.com", cfg.AccountID)
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: true,
		Region: "auto",
	})
	if err != nil {
		return nil, fmt.Errorf("storage r2: new client: %w", err)
	}
	return &R2{client: client, bucket: cfg.Bucket}, nil
}

func (r *R2) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = time.Hour
	}
	u, err := r.client.PresignedGetObject(ctx, r.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("storage r2: presign get: %w", err)
	}
	return u.String(), nil
}

// SignedPutURL mints a short-lived S3 presigned PUT URL. The client uploads
// the object body directly to R2 with that URL; the API never proxies bytes.
// The contentType arg is reserved for callers that need to enforce a header;
// minio's PresignedPutObject does not bind a Content-Type into the signature,
// so callers that want to force a header should validate it server-side after
// upload or wrap with a multipart POST policy in a later phase.
func (r *R2) SignedPutURL(ctx context.Context, key string, ttl time.Duration, _ string) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	u, err := r.client.PresignedPutObject(ctx, r.bucket, key, ttl)
	if err != nil {
		return "", fmt.Errorf("storage r2: presign put: %w", err)
	}
	return u.String(), nil
}

func (r *R2) Exists(ctx context.Context, key string) (bool, error) {
	_, err := r.client.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	errResp := minio.ToErrorResponse(err)
	if errResp.StatusCode == 404 || errResp.Code == "NoSuchKey" {
		return false, nil
	}
	return false, fmt.Errorf("storage r2: stat: %w", err)
}

func (r *R2) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := r.client.PutObject(ctx, r.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("storage r2: put: %w", err)
	}
	return nil
}
