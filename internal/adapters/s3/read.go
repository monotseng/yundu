package s3

import (
	"context"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"yundu/internal/integrations"
)

func OpenVersion(ctx context.Context, cfg integrations.S3Config, objectKey, versionID, accessKey, secretKey string) (io.ReadCloser, int64, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: cfg.UseTLS, Region: cfg.Region, BucketLookup: bucketLookup(cfg.PathStyle)})
	if err != nil {
		return nil, 0, err
	}
	object, err := client.GetObject(ctx, cfg.Bucket, objectKey, minio.GetObjectOptions{VersionID: versionID})
	if err != nil {
		return nil, 0, err
	}
	info, err := object.Stat()
	if err != nil {
		object.Close()
		return nil, 0, err
	}
	return object, info.Size, nil
}
