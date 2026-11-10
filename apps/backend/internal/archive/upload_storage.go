package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type UploadGrant struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Fields    map[string]string `json:"fields"`
	ExpiresAt string            `json:"expiresAt"`
}
type OriginalStorage interface {
	Grant(context.Context, string, time.Time, int64) (UploadGrant, error)
	Read(context.Context, string, int64) ([]byte, error)
	PutOriginal(context.Context, string, []byte) error
}

// S3Storage keeps both staging and originals in a private bucket. Its grant is
// a POST policy constrained to one exact key and a bounded object length.
type S3Storage struct {
	client *minio.Client
	bucket string
}

func StorageFromEnv() (*S3Storage, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		endpoint = "127.0.0.1:8333"
	}
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	access, secret := os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY")
	if access == "" || secret == "" {
		return nil, errors.New("S3 credentials required")
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: os.Getenv("S3_SECURE") == "true"})
	if err != nil {
		return nil, err
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		bucket = "invoyis-originals"
	}
	return &S3Storage{client: client, bucket: bucket}, nil
}
func (s *S3Storage) Grant(ctx context.Context, key string, expiry time.Time, cap int64) (UploadGrant, error) {
	policy := minio.NewPostPolicy()
	if err := policy.SetBucket(s.bucket); err != nil {
		return UploadGrant{}, err
	}
	if err := policy.SetKey(key); err != nil {
		return UploadGrant{}, err
	}
	if err := policy.SetExpires(expiry); err != nil {
		return UploadGrant{}, err
	}
	if err := policy.SetContentLengthRange(1, cap); err != nil {
		return UploadGrant{}, err
	}
	u, fields, err := s.client.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return UploadGrant{}, err
	}
	// Browser endpoint may differ from the private server endpoint only by host.
	if public := os.Getenv("S3_BROWSER_ENDPOINT"); public != "" {
		dest, e := url.Parse(public)
		if e != nil {
			return UploadGrant{}, e
		}
		u.Scheme = dest.Scheme
		u.Host = dest.Host
	}
	return UploadGrant{Method: "POST", URL: u.String(), Headers: map[string]string{}, Fields: fields, ExpiresAt: expiry.UTC().Format(time.RFC3339)}, nil
}
func (s *S3Storage) Read(ctx context.Context, key string, cap int64) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, cap+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > cap {
		return nil, ErrInvalid
	}
	return data, nil
}
func (s *S3Storage) PutOriginal(ctx context.Context, key string, data []byte) error {
	opts := minio.PutObjectOptions{ContentType: "application/pdf"}
	opts.SetMatchETagExcept("*")
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), opts)
	return err
}
