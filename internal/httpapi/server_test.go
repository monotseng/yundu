package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"yundu/internal/config"
)

func TestOriginGuardDelegatesOnlyDownloadCapabilityPost(t *testing.T) {
	s := &Server{cfg: config.Config{Environment: "production"}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	download := httptest.NewRequest(http.MethodPost, "http://office.localhost:9080/data/v1/downloads", nil)
	download.Header.Set("Origin", "https://rewritten.invalid")
	downloadResult := httptest.NewRecorder()
	s.originGuard(next).ServeHTTP(downloadResult, download)
	if downloadResult.Code != http.StatusNoContent {
		t.Fatalf("download capability post was blocked: %d", downloadResult.Code)
	}

	writeAPI := httptest.NewRequest(http.MethodPost, "http://office.localhost:9080/api/v1/requests", nil)
	writeAPI.Header.Set("Origin", "https://rewritten.invalid")
	writeResult := httptest.NewRecorder()
	s.originGuard(next).ServeHTTP(writeResult, writeAPI)
	if writeResult.Code != http.StatusForbidden {
		t.Fatalf("ordinary write API bypassed origin policy: %d", writeResult.Code)
	}
}

func TestProductionRejectsInvalidIdentityMasterKey(t *testing.T) {
	cfg := config.Config{Environment: "production", Security: config.Security{MasterKey: "not-base64"}}
	if _, err := New(cfg, nil, "test"); err == nil {
		t.Fatal("production accepted invalid identity master key")
	}
}

func TestSameOriginNavigationRequiresBrowserEvidence(t *testing.T) {
	r := httptest.NewRequest("POST", "http://office.localhost:9080/data/v1/downloads", nil)
	r.Header.Set("Origin", "null")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Referer", "http://office.localhost:9080/downloads")
	if !sameOriginNavigation("http://office.localhost:9080", r) {
		t.Fatal("same-origin form navigation was rejected")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if sameOriginNavigation("http://office.localhost:9080", r) {
		t.Fatal("cross-site form navigation was accepted")
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Referer", "http://production.localhost:9080/downloads")
	if sameOriginNavigation("http://office.localhost:9080", r) {
		t.Fatal("navigation from another portal was accepted")
	}
}

func TestSameOriginNormalizesSafeURLVariants(t *testing.T) {
	tests := []struct {
		configured string
		received   string
		want       bool
	}{
		{"http://office.localhost:9080", "http://office.localhost:9080", true},
		{"http://office.localhost:9080", "http://OFFICE.localhost:9080/", true},
		{"http://office.localhost", "http://office.localhost:80", true},
		{"https://office.example", "https://office.example:443", true},
		{"http://office.localhost:9080", "http://production.localhost:9080", false},
		{"http://office.localhost:9080", "https://office.localhost:9080", false},
		{"http://office.localhost:9080", "http://office.localhost:9081", false},
		{"http://office.localhost:9080", "http://office.localhost:9080/path", false},
	}
	for _, tt := range tests {
		if got := sameOrigin(tt.configured, tt.received); got != tt.want {
			t.Errorf("sameOrigin(%q, %q)=%v want %v", tt.configured, tt.received, got, tt.want)
		}
	}
}
