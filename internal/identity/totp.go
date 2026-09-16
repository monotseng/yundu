package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // TOTP interoperability profile fixed by the product baseline.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	TOTPPeriod = int64(30)
	TOTPDigits = 6
)

var ErrTOTPReplay = errors.New("TOTP time step was already consumed")

func NewTOTPSecret() ([]byte, error) {
	secret := make([]byte, 20)
	_, err := rand.Read(secret)
	return secret, err
}

func EncodeTOTPSecret(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

func TOTPURI(username string, secret []byte) string {
	issuer := "云渡"
	label := issuer + ":" + username
	values := url.Values{"secret": {EncodeTOTPSecret(secret)}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + url.PathEscape(label) + "?" + values.Encode()
}

func TOTPCode(secret []byte, step int64, digits int) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, value%mod)
}

func MatchTOTP(secret []byte, code string, now time.Time, lastUsedStep int64) (int64, error) {
	if len(code) != TOTPDigits {
		return 0, errors.New("invalid TOTP code")
	}
	if _, err := strconv.Atoi(code); err != nil {
		return 0, errors.New("invalid TOTP code")
	}
	current := now.UTC().Unix() / TOTPPeriod
	for _, step := range []int64{current, current - 1, current + 1} {
		expected := TOTPCode(secret, step, TOTPDigits)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			if step <= lastUsedStep {
				return 0, ErrTOTPReplay
			}
			return step, nil
		}
	}
	return 0, errors.New("invalid TOTP code")
}

func DecodeTOTPSecret(value string) ([]byte, error) {
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(value)))
}
