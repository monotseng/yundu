package secrets

import (
	"encoding/base64"
	"testing"
)

func TestBoxRoundTripAndContextBinding(t *testing.T) {
	box, err := NewBox(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("TOTP-SECRET"), "user:1")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Open(sealed, "user:1")
	if err != nil || string(plain) != "TOTP-SECRET" {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	if _, err := box.Open(sealed, "user:2"); err == nil {
		t.Fatal("ciphertext opened under wrong context")
	}
}
