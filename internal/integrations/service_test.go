package integrations

import "testing"

func TestValidateS3Config(t *testing.T) {
	valid := S3Config{Endpoint: "minio.internal:9000", Bucket: "yundu-office", Prefix: "exchange", VersioningRequired: true, AccessKeySecretID: "a", SecretKeySecretID: "b"}
	if err := ValidateS3Config(valid); err != nil {
		t.Fatal(err)
	}
	valid.Prefix = "../escape"
	if err := ValidateS3Config(valid); err == nil {
		t.Fatal("unsafe prefix accepted")
	}
	valid.Prefix = "safe"
	valid.VersioningRequired = false
	if err := ValidateS3Config(valid); err == nil {
		t.Fatal("versioning disable accepted")
	}
}
