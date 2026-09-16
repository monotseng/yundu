package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"yundu/internal/integrations"
)

type UploadResult struct {
	VersionID string `json:"version_id"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
}

func Upload(ctx context.Context, cfg integrations.S3Config, objectKey, contentType string, body io.Reader, size int64, accessKey, secretKey string) (UploadResult, error) {
	if err := integrations.ValidateS3Config(cfg); err != nil {
		return UploadResult{}, err
	}
	if size <= 0 {
		return UploadResult{}, errors.New("invalid content length")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: cfg.UseTLS, Region: cfg.Region, BucketLookup: bucketLookup(cfg.PathStyle)})
	if err != nil {
		return UploadResult{}, err
	}
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(body, size+1), hash)
	info, err := client.PutObject(ctx, cfg.Bucket, objectKey, reader, size, minio.PutObjectOptions{ContentType: contentType, DisableMultipart: size < 5*1024*1024})
	if err != nil {
		return UploadResult{}, err
	}
	if info.Size != size {
		return UploadResult{}, errors.New("uploaded size mismatch")
	}
	if cfg.VersioningRequired && info.VersionID == "" {
		return UploadResult{}, errors.New("object version id missing")
	}
	return UploadResult{VersionID: info.VersionID, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: info.Size}, nil
}
