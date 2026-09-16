//go:build integration

package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"yundu/internal/secrets"
)

func TestMySQLConcurrentTOTPConsumption(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	box, err := secrets.NewBox(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	userID, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	username := "integration-" + hex.EncodeToString(userID)
	password := "Integration-Passphrase-2026"
	passwordHash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("12345678901234567890")
	ciphertext, err := box.Seal(secret, "totp:"+hex.EncodeToString(userID))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO user_accounts(id,username,display_name,password_hash,status,mfa_state,mfa_secret_ciphertext) VALUES(?,?,?,?, 'ACTIVE','ACTIVE',?)`, userID, username, "集成测试", passwordHash, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM audit_events WHERE actor_id=?", userID)
		db.Exec("DELETE FROM sessions WHERE user_id=?", userID)
		db.Exec("DELETE FROM login_challenges WHERE user_id=?", userID)
		db.Exec("DELETE FROM user_accounts WHERE id=?", userID)
	})
	svc := NewService(db, box)
	fixed := time.Now().UTC().Truncate(time.Second)
	svc.now = func() time.Time { return fixed }
	binding := []byte("test-browser")
	challenge, _, err := svc.BeginPassword(context.Background(), username, password, "OFFICE", binding, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	code := TOTPCode(secret, fixed.Unix()/30, 6)
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.CompleteTOTP(context.Background(), challenge, code, "OFFICE", binding, "127.0.0.1")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one successful consumption, got %d", success)
	}
}

func TestMySQLActivationBindingAndSessionLifecycle(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	box, err := secrets.NewBox(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := randomID()
	activationID, _ := randomID()
	username := "activate-" + hex.EncodeToString(userID)
	rawActivation, activationHash, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,?,'PENDING_ACTIVATION','UNBOUND')`, userID, username, "激活测试")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO activation_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'ACTIVATE',?)`, activationID, userID, activationHash, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM audit_events WHERE actor_id=?", userID)
		db.Exec("DELETE FROM sessions WHERE user_id=?", userID)
		db.Exec("DELETE FROM login_challenges WHERE user_id=?", userID)
		db.Exec("DELETE FROM mfa_binding_tokens WHERE user_id=?", userID)
		db.Exec("DELETE FROM activation_tokens WHERE user_id=?", userID)
		db.Exec("DELETE FROM user_accounts WHERE id=?", userID)
	})
	svc := NewService(db, box)
	fixed := time.Now().UTC().Truncate(time.Second)
	svc.now = func() time.Time { return fixed }
	binding, err := svc.Activate(context.Background(), rawActivation, "Activation-Passphrase-2026")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := DecodeTOTPSecret(binding.Secret)
	if err != nil {
		t.Fatal(err)
	}
	code := TOTPCode(secret, fixed.Unix()/30, 6)
	if err = svc.ConfirmBinding(context.Background(), binding.Token, code); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.BeginPassword(context.Background(), username, "Activation-Passphrase-2026", "OFFICE", []byte("browser"), "127.0.0.2"); err != nil {
		t.Fatal(err)
	}
	fixed = fixed.Add(30 * time.Second)
	challenge, _, err := svc.BeginPassword(context.Background(), username, "Activation-Passphrase-2026", "OFFICE", []byte("browser"), "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.CompleteTOTP(context.Background(), challenge, TOTPCode(secret, fixed.Unix()/30, 6), "OFFICE", []byte("browser"), "127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(context.Background(), session.Token, "PRODUCTION"); err == nil {
		t.Fatal("session crossed portal zone")
	}
	if _, err = svc.Authenticate(context.Background(), session.Token, "OFFICE"); err != nil {
		t.Fatal(err)
	}
	if err = svc.Logout(context.Background(), session.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Authenticate(context.Background(), session.Token, "OFFICE"); err == nil {
		t.Fatal("revoked session remained valid")
	}
	for i := 0; i < 10; i++ {
		_, _, _ = svc.BeginPassword(context.Background(), username, "Wrong-Password-For-Limit", "OFFICE", []byte("browser"), "127.0.0.9")
	}
	if _, _, err = svc.BeginPassword(context.Background(), username, "Activation-Passphrase-2026", "OFFICE", []byte("browser"), "127.0.0.9"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected account rate limit, got %v", err)
	}
}

func TestMySQLTwoPersonMFARecovery(t *testing.T) {
	dsn := os.Getenv("YUNDU_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("YUNDU_TEST_MYSQL_DSN not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	box, err := secrets.NewBox(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, box)
	now := time.Now().UTC().Truncate(time.Second)
	svc.now = func() time.Time { return now }
	orgRoleID, _ := randomID()
	secRoleID, _ := randomID()
	_, _ = db.Exec("INSERT IGNORE INTO roles(id,code,name) VALUES(?,'ORG_ADMIN','组织管理员'),(?,'SECURITY_ADMIN','安全管理员')", orgRoleID, secRoleID)
	if err = db.QueryRow("SELECT id FROM roles WHERE code='ORG_ADMIN'").Scan(&orgRoleID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT id FROM roles WHERE code='SECURITY_ADMIN'").Scan(&secRoleID); err != nil {
		t.Fatal(err)
	}
	targetID, _ := randomID()
	orgID, _ := randomID()
	secID, _ := randomID()
	passwordHash, _ := HashPassword("Recovery-Target-Passphrase-2026")
	prefix := hex.EncodeToString(targetID)
	for _, u := range []struct {
		id                  []byte
		name, display, hash string
	}{{targetID, "target-" + prefix, "目标用户", passwordHash}, {orgID, "org-" + prefix, "组织管理员", passwordHash}, {secID, "sec-" + prefix, "安全管理员", passwordHash}} {
		if _, err = db.Exec("INSERT INTO user_accounts(id,username,display_name,password_hash,status,mfa_state) VALUES(?,?,?,?, 'ACTIVE','ACTIVE')", u.id, u.name, u.display, u.hash); err != nil {
			t.Fatal(err)
		}
	}
	orgAssign, _ := randomID()
	secAssign, _ := randomID()
	_, err = db.Exec("INSERT INTO user_role_assignments(id,user_id,role_id,scope_type) VALUES(?,?,?,'GLOBAL'),(?,?,?,'GLOBAL')", orgAssign, orgID, orgRoleID, secAssign, secID, secRoleID)
	if err != nil {
		t.Fatal(err)
	}
	makeSession := func(userID []byte) (string, []byte) {
		raw, hash, _ := newToken()
		id, _ := randomID()
		_, err = db.Exec("INSERT INTO sessions(id,user_id,token_hash,portal_zone,session_version,mfa_verified_at,last_seen_at,expires_at) VALUES(?,?,?,'OFFICE',1,?,?,?)", id, userID, hash, now, now, now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		return raw, id
	}
	orgToken, _ := makeSession(orgID)
	secToken, _ := makeSession(secID)
	_, targetSessionID := makeSession(targetID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM audit_events WHERE actor_id IN (?,?,?)", targetID, orgID, secID)
		db.Exec("DELETE FROM mfa_binding_tokens WHERE user_id IN (?,?,?)", targetID, orgID, secID)
		db.Exec("DELETE FROM activation_tokens WHERE user_id IN (?,?,?)", targetID, orgID, secID)
		db.Exec("DELETE FROM mfa_recovery_requests WHERE user_id=?", targetID)
		db.Exec("DELETE FROM sessions WHERE user_id IN (?,?,?)", targetID, orgID, secID)
		db.Exec("DELETE FROM user_role_assignments WHERE user_id IN (?,?)", orgID, secID)
		db.Exec("DELETE FROM user_accounts WHERE id IN (?,?,?)", targetID, orgID, secID)
	})
	recovery, err := svc.InitiateRecovery(context.Background(), orgToken, "OFFICE", hex.EncodeToString(targetID), "已在线下核验工牌及负责人确认")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.ApproveRecovery(context.Background(), orgToken, "OFFICE", recovery.ID, "不能由发起人自行审核本次恢复", 1); err == nil {
		t.Fatal("initiator approved own recovery")
	}
	rebindToken, _, err := svc.ApproveRecovery(context.Background(), secToken, "OFFICE", recovery.ID, "安全管理员完成独立身份复核", 1)
	if err != nil {
		t.Fatal(err)
	}
	var revoked sql.NullTime
	if err = db.QueryRow("SELECT revoked_at FROM sessions WHERE id=?", targetSessionID).Scan(&revoked); err != nil || !revoked.Valid {
		t.Fatalf("old session not revoked: %v", err)
	}
	binding, err := svc.BeginRebind(context.Background(), rebindToken, "Recovery-Target-Passphrase-2026")
	if err != nil {
		t.Fatal(err)
	}
	secret, err := DecodeTOTPSecret(binding.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.ConfirmBinding(context.Background(), binding.Token, TOTPCode(secret, now.Unix()/30, 6)); err != nil {
		t.Fatal(err)
	}
}
