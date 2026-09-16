package identity

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRFC6238SHA1Vector(t *testing.T) {
	secret := []byte("12345678901234567890")
	if got := TOTPCode(secret, 59/30, 8); got != "94287082" {
		t.Fatalf("got %s", got)
	}
}

func TestTOTPReplayAndWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1710000000, 0)
	code := TOTPCode(secret, now.Unix()/30, 6)
	step, err := MatchTOTP(secret, code, now, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MatchTOTP(secret, code, now, step); !errorsIs(err, ErrTOTPReplay) {
		t.Fatalf("expected replay, got %v", err)
	}
}

func TestTOTPURIUsesInternalEncodedLabel(t *testing.T) {
	uri := TOTPURI("zhang.san", []byte("12345678901234567890"))
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" || !strings.Contains(parsed.EscapedPath(), "%E4%BA%91%E6%B8%A1") {
		t.Fatalf("unexpected URI %s", uri)
	}
	if parsed.Query().Get("issuer") != "云渡" || parsed.Query().Get("digits") != "6" {
		t.Fatalf("unexpected query %s", parsed.RawQuery)
	}
}

func errorsIs(err, target error) bool { return err == target }
