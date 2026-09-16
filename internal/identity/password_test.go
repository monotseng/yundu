package identity

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("正确的长密码-Passphrase-2026")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("正确的长密码-Passphrase-2026", hash)
	if err != nil || !ok {
		t.Fatalf("verify failed: %v", err)
	}
	ok, err = VerifyPassword("错误的长密码-Passphrase-2026", hash)
	if err != nil || ok {
		t.Fatalf("wrong password result: %v %v", ok, err)
	}
}

func TestPasswordIsNotTrimmed(t *testing.T) {
	hash, err := HashPassword("  leading-space-password")
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := VerifyPassword("leading-space-password", hash)
	if ok {
		t.Fatal("password was unexpectedly trimmed")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, password := range []string{"short", string(make([]byte, 513))} {
		if ValidatePassword(password) == nil {
			t.Fatalf("accepted invalid password length %d", len(password))
		}
	}
}
