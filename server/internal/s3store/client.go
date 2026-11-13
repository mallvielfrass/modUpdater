package s3store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	s3svc "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type Settings struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	Secret    string
}

type Blob interface {
	HeadBucket(ctx context.Context) error
	ListPrefix(ctx context.Context, prefix string) error
	Put(ctx context.Context, key string, body io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
}

type Client struct {
	api    *s3svc.Client
	bucket string
}

func New(s Settings) (*Client, error) {
	region := strings.TrimSpace(s.Region)
	if region == "" {
		region = "us-east-1"
	}
	cfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(s.AccessKey, s.Secret, ""),
		HTTPClient:  &http.Client{Timeout: 2 * time.Minute},
	}
	endpoint := strings.TrimSpace(s.Endpoint)
	api := s3svc.NewFromConfig(cfg, func(o *s3svc.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	})
	return &Client{api: api, bucket: strings.TrimSpace(s.Bucket)}, nil
}

func Key(prefix, rest string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	rest = strings.TrimLeft(rest, "/")
	if prefix == "" {
		return rest
	}
	return prefix + "/" + rest
}

func ChunkKey(prefix, hash string) string {
	return Key(prefix, "chunks/"+hash[:2]+"/"+hash[2:])
}

func ManifestKey(prefix, id string) string {
	return Key(prefix, "manifests/"+id+".json")
}

func Probe(ctx context.Context, b Blob, prefix string) error {
	if err := b.HeadBucket(ctx); err != nil {
		if listErr := b.ListPrefix(ctx, strings.Trim(prefix, "/")); listErr != nil {
			return err
		}
	}
	key := Key(prefix, ".modupdater-probe")
	if err := b.Put(ctx, key, bytes.NewReader([]byte("ok")), 2); err != nil {
		return err
	}
	return b.Delete(ctx, key)
}

func (c *Client) HeadBucket(ctx context.Context) error {
	_, err := c.api.HeadBucket(ctx, &s3svc.HeadBucketInput{Bucket: aws.String(c.bucket)})
	return err
}

func (c *Client) ListPrefix(ctx context.Context, prefix string) error {
	_, err := c.api.ListObjectsV2(ctx, &s3svc.ListObjectsV2Input{
		Bucket:  aws.String(c.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(1),
	})
	return err
}

func (c *Client) Put(ctx context.Context, key string, body io.Reader, size int64) error {
	in := &s3svc.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	_, err := c.api.PutObject(ctx, in)
	return err
}

func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.api.DeleteObject(ctx, &s3svc.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.api.HeadObject(ctx, &s3svc.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if missingObject(err) {
		return false, nil
	}
	return false, err
}

func (c *Client) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.api.GetObject(ctx, &s3svc.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func missingObject(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "NotFound", "NoSuchKey", "404":
			return true
		}
	}
	return false
}
