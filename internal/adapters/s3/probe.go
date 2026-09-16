package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"yundu/internal/integrations"
)

type ProbeResult struct {
	VersionID string `json:"version_id"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
}

func Probe(ctx context.Context, cfg integrations.S3Config, accessKey, secretKey string) (ProbeResult, error) {
	if err := integrations.ValidateS3Config(cfg); err != nil {
		return ProbeResult{}, err
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: cfg.UseTLS, Region: cfg.Region, BucketLookup: bucketLookup(cfg.PathStyle)})
	if err != nil {
		return ProbeResult{}, err
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil || !exists {
		if err == nil {
			err = errors.New("bucket does not exist")
		}
		return ProbeResult{}, fmt.Errorf("BUCKET_CHECK: %w", err)
	}
	versioning, err := client.GetBucketVersioning(ctx, cfg.Bucket)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("VERSIONING_CHECK: %w", err)
	}
	if cfg.VersioningRequired && versioning.Status != "Enabled" {
		return ProbeResult{}, errors.New("VERSIONING_REQUIRED: bucket versioning is not enabled")
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return ProbeResult{}, err
	}
	objectName := strings.TrimSuffix(cfg.Prefix, "/") + "/_yundu_probe/" + hex.EncodeToString(random)
	objectName = strings.TrimPrefix(objectName, "/")
	payload := make([]byte, 1024)
	if _, err = rand.Read(payload); err != nil {
		return ProbeResult{}, err
	}
	expected := sha256.Sum256(payload)
	info, err := client.PutObject(ctx, cfg.Bucket, objectName, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		return ProbeResult{}, fmt.Errorf("PROBE_PUT: %w", err)
	}
	if info.VersionID == "" && cfg.VersioningRequired {
		return ProbeResult{}, errors.New("PROBE_PUT: object version id missing")
	}
	defer client.RemoveObject(context.Background(), cfg.Bucket, objectName, minio.RemoveObjectOptions{VersionID: info.VersionID, GovernanceBypass: true})
	object, err := client.GetObject(ctx, cfg.Bucket, objectName, minio.GetObjectOptions{VersionID: info.VersionID})
	if err != nil {
		return ProbeResult{}, fmt.Errorf("PROBE_GET: %w", err)
	}
	defer object.Close()
	actual := sha256.New()
	n, err := io.Copy(actual, object)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("PROBE_READ: %w", err)
	}
	if n != int64(len(payload)) || !bytes.Equal(actual.Sum(nil), expected[:]) {
		return ProbeResult{}, errors.New("PROBE_INTEGRITY: hash or size mismatch")
	}
	if err = client.RemoveObject(ctx, cfg.Bucket, objectName, minio.RemoveObjectOptions{VersionID: info.VersionID, GovernanceBypass: true}); err != nil {
		return ProbeResult{}, fmt.Errorf("PROBE_DELETE: %w", err)
	}
	return ProbeResult{VersionID: info.VersionID, SHA256: hex.EncodeToString(expected[:]), Bytes: n}, nil
}
func bucketLookup(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}
