package s3

import (
	"context"
	"errors"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"yundu/internal/integrations"
)

func DeleteVersion(ctx context.Context, cfg integrations.S3Config, objectKey, versionID, accessKey, secretKey string) error {
	if objectKey == "" || versionID == "" {
		return errors.New("exact object version is required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: cfg.UseTLS, Region: cfg.Region, BucketLookup: bucketLookup(cfg.PathStyle)})
	if err != nil {
		return err
	}
	if err = client.RemoveObject(ctx, cfg.Bucket, objectKey, minio.RemoveObjectOptions{VersionID: versionID}); err != nil {
		return err
	}
	_, err = client.StatObject(ctx, cfg.Bucket, objectKey, minio.StatObjectOptions{VersionID: versionID})
	if err == nil {
		return errors.New("object version still exists after deletion")
	}
	response := minio.ToErrorResponse(err)
	if response.Code != "NoSuchKey" && response.Code != "NoSuchVersion" && response.StatusCode != 404 {
		return err
	}
	return nil
}
