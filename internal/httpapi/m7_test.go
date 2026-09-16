package httpapi

import (
	"strings"
	"testing"
)

func TestContentDispositionHasFallbackAndUTF8Name(t *testing.T) {
	value := contentDisposition("生产 数据.csv")
	if !strings.Contains(value, `filename="download.csv"`) {
		t.Fatalf("missing ASCII fallback: %s", value)
	}
	if !strings.Contains(value, "filename*=utf-8''") {
		t.Fatalf("missing UTF-8 filename: %s", value)
	}
	if strings.ContainsAny(value, "\r\n") {
		t.Fatal("header injection")
	}
}
