package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"yundu/internal/secrets"
)

var (
	ErrInvalidCredentials = errors.New("invalid username, password, or verification code")
	ErrChallengeExpired   = errors.New("login challenge expired")
)
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

type Service struct {
	db  *sql.DB
	box *secrets.Box
	now func() time.Time
}
type Session struct {
	Token       string
	ExpiresAt   time.Time
	UserID      string
	DisplayName string
}
type Binding struct {
	Token, Secret, URI string
	ExpiresAt          time.Time
}
type CurrentUser struct {
	ID            string    `json:"id"`
	Username      string    `json:"username"`
	DisplayName   string    `json:"display_name"`
	AvatarEmoji   string    `json:"avatar_emoji"`
	PortalZone    string    `json:"portal_zone"`
	MFAVerifiedAt time.Time `json:"mfa_verified_at"`
	ExpiresAt     time.Time `json:"expires_at"`
}

func NewService(db *sql.DB, box *secrets.Box) *Service {
	return &Service{db: db, box: box, now: time.Now}
}

func (s *Service) BootstrapAdmin(ctx context.Context, username, displayName string) (string, time.Time, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	displayName = strings.TrimSpace(displayName)
	if !usernamePattern.MatchString(username) || displayName == "" || len([]rune(displayName)) > 128 {
		return "", time.Time{}, errors.New("invalid bootstrap account")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_accounts").Scan(&count); err != nil {
		return "", time.Time{}, err
	}
	if count != 0 {
		return "", time.Time{}, errors.New("bootstrap is only allowed when no users exist")
	}
	userID, _ := randomID()
	roleID, _ := randomID()
	orgRoleID, _ := randomID()
	assignmentID, _ := randomID()
	orgAssignmentID, _ := randomID()
	tokenID, _ := randomID()
	raw, hash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().UTC().Add(24 * time.Hour)
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_accounts(id,username,display_name,status,mfa_state) VALUES(?,?,?,'PENDING_ACTIVATION','UNBOUND')", userID, username, displayName); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO roles(id,code,name) VALUES(?,'SECURITY_ADMIN','安全管理员')", roleID); err != nil {
		return "", time.Time{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code='SECURITY_ADMIN'").Scan(&roleID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO roles(id,code,name) VALUES(?,'ORG_ADMIN','组织管理员')", orgRoleID); err != nil {
		return "", time.Time{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code='ORG_ADMIN'").Scan(&orgRoleID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO role_permissions(role_id,permission) VALUES(?,'system.bootstrap'),(?,'identity.manage'),(?,'security.manage'),(?,'integration.manage'),(?,'integration.publish'),(?,'secret.manage'),(?,'workflow.manage'),(?,'workflow.publish'),(?,'approval.admin_on_behalf'),(?,'request.revoke'),(?,'audit.read')", roleID, roleID, roleID, roleID, roleID, roleID, roleID, roleID, roleID, roleID, roleID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO role_permissions(role_id,permission) VALUES(?,'organization.manage'),(?,'user.manage'),(?,'integration.read')", orgRoleID, orgRoleID, orgRoleID); err != nil {
		return "", time.Time{}, err
	}
	for _, catalog := range []struct {
		code, name  string
		permissions []string
	}{
		{"REQUESTER", "申请人", []string{"request.create", "request.read.own", "download.own"}},
		{"GROUP_MANAGER", "团队负责人", []string{"approval.group", "request.read.group"}},
		{"DEPARTMENT_MANAGER", "部门负责人", []string{"approval.department", "request.read.department"}},
		{"SECURITY_OFFICER", "安全审批员", []string{"approval.security", "request.read.security"}},
		{"AUDITOR", "审计员", []string{"audit.read"}},
		{"SYSTEM_OPERATOR", "系统运维员", []string{"operations.read", "operations.manage"}},
	} {
		catalogID, _ := randomID()
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO roles(id,code,name) VALUES(?,?,?)", catalogID, catalog.code, catalog.name); err != nil {
			return "", time.Time{}, err
		}
		if err = tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code=?", catalog.code).Scan(&catalogID); err != nil {
			return "", time.Time{}, err
		}
		for _, permission := range catalog.permissions {
			if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO role_permissions(role_id,permission) VALUES(?,?)", catalogID, permission); err != nil {
				return "", time.Time{}, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_role_assignments(id,user_id,role_id,scope_type) VALUES(?,?,?,'GLOBAL')", assignmentID, userID, roleID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_role_assignments(id,user_id,role_id,scope_type) VALUES(?,?,?,'GLOBAL')", orgAssignmentID, userID, orgRoleID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO activation_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'ACTIVATE',?)", tokenID, userID, hash, expires); err != nil {
		return "", time.Time{}, err
	}
	if err = auditIdentityTx(ctx, tx, userID, "IDENTITY_BOOTSTRAP", "SUCCESS", map[string]any{"username": username}, s.now().UTC()); err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return raw, expires, nil
}

func (s *Service) Activate(ctx context.Context, activationToken, password string) (Binding, error) {
	if err := ValidatePassword(password); err != nil {
		return Binding{}, err
	}
	activationHash := sha256.Sum256([]byte(activationToken))
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	var activationID, userID []byte
	var expires time.Time
	var consumed sql.NullTime
	var status, username string
	err = tx.QueryRowContext(ctx, `SELECT a.id,a.user_id,a.expires_at,a.consumed_at,u.status,u.username FROM activation_tokens a JOIN user_accounts u ON u.id=a.user_id WHERE a.token_hash=? AND a.purpose='ACTIVATE' FOR UPDATE`, activationHash[:]).Scan(&activationID, &userID, &expires, &consumed, &status, &username)
	if err != nil || consumed.Valid || !now.Before(expires) || status != "PENDING_ACTIVATION" {
		return Binding{}, ErrInvalidCredentials
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return Binding{}, err
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return Binding{}, err
	}
	ciphertext, err := s.box.Seal(secret, "totp:"+hex.EncodeToString(userID))
	if err != nil {
		return Binding{}, err
	}
	bindingID, _ := randomID()
	rawBinding, bindingHash, err := newToken()
	if err != nil {
		return Binding{}, err
	}
	bindingExpiry := now.Add(10 * time.Minute)
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET password_hash=?,password_changed_at=?,status='MFA_BIND_REQUIRED',mfa_state='PENDING',mfa_secret_ciphertext=? WHERE id=?", passwordHash, now, ciphertext, userID); err != nil {
		return Binding{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE activation_tokens SET consumed_at=? WHERE id=?", now, activationID); err != nil {
		return Binding{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO mfa_binding_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'INITIAL_BIND',?)", bindingID, userID, bindingHash, bindingExpiry); err != nil {
		return Binding{}, err
	}
	if err = auditIdentityTx(ctx, tx, userID, "ACCOUNT_ACTIVATED_PASSWORD_SET", "SUCCESS", map[string]any{"mfa_state": "PENDING"}, now); err != nil {
		return Binding{}, err
	}
	if err = tx.Commit(); err != nil {
		return Binding{}, err
	}
	return Binding{Token: rawBinding, Secret: EncodeTOTPSecret(secret), URI: TOTPURI(username, secret), ExpiresAt: bindingExpiry}, nil
}

func (s *Service) ConfirmBinding(ctx context.Context, bindingToken, code string) error {
	hash := sha256.Sum256([]byte(bindingToken))
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var bindingID, userID, ciphertext []byte
	var expires time.Time
	var consumed sql.NullTime
	var attempts int
	var status string
	err = tx.QueryRowContext(ctx, `SELECT b.id,b.user_id,b.expires_at,b.consumed_at,b.attempts,u.status,u.mfa_secret_ciphertext FROM mfa_binding_tokens b JOIN user_accounts u ON u.id=b.user_id WHERE b.token_hash=? FOR UPDATE`, hash[:]).Scan(&bindingID, &userID, &expires, &consumed, &attempts, &status, &ciphertext)
	if err != nil || consumed.Valid || attempts >= 5 || !now.Before(expires) || status != "MFA_BIND_REQUIRED" {
		return ErrInvalidCredentials
	}
	secret, err := s.box.Open(ciphertext, "totp:"+hex.EncodeToString(userID))
	if err != nil {
		return err
	}
	step, err := MatchTOTP(secret, code, now, -1)
	if err != nil {
		_, _ = tx.ExecContext(ctx, "UPDATE mfa_binding_tokens SET attempts=attempts+1 WHERE id=?", bindingID)
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		return ErrInvalidCredentials
	}
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET status='ACTIVE',mfa_state='ACTIVE',mfa_last_used_step=?,session_version=session_version+1 WHERE id=?", step, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE mfa_binding_tokens SET consumed_at=? WHERE id=?", now, bindingID); err != nil {
		return err
	}
	if err = auditIdentityTx(ctx, tx, userID, "MFA_BOUND", "SUCCESS", map[string]any{"purpose": "INITIAL_OR_REBIND"}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Authenticate(ctx context.Context, token, zone string) (CurrentUser, error) {
	hash := sha256.Sum256([]byte(token))
	now := s.now().UTC()
	var user CurrentUser
	var id []byte
	var status string
	var storedVersion, currentVersion uint64
	var revoked sql.NullTime
	var lastSeen time.Time
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.display_name,u.avatar_emoji,u.status,u.session_version,s.session_version,s.portal_zone,s.mfa_verified_at,s.last_seen_at,s.expires_at,s.revoked_at FROM sessions s JOIN user_accounts u ON u.id=s.user_id WHERE s.token_hash=?`, hash[:]).Scan(&id, &user.Username, &user.DisplayName, &user.AvatarEmoji, &status, &currentVersion, &storedVersion, &user.PortalZone, &user.MFAVerifiedAt, &lastSeen, &user.ExpiresAt, &revoked)
	if err != nil || status != "ACTIVE" || revoked.Valid || storedVersion != currentVersion || user.PortalZone != zone || !now.Before(user.ExpiresAt) || now.Sub(lastSeen) > 30*time.Minute {
		return CurrentUser{}, ErrInvalidCredentials
	}
	user.ID = hex.EncodeToString(id)
	_, _ = s.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at=? WHERE token_hash=?", now, hash[:])
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL", s.now().UTC(), hash[:])
	return err
}

func (s *Service) LogoutAll(ctx context.Context, token, zone string) error {
	user, err := s.Authenticate(ctx, token, zone)
	if err != nil {
		return err
	}
	id, err := hex.DecodeString(user.ID)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET session_version=session_version+1 WHERE id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", now, id); err != nil {
		return err
	}
	if err = auditIdentityTx(ctx, tx, id, "LOGOUT_ALL", "SUCCESS", map[string]any{}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ChangePassword(ctx context.Context, token, zone, currentPassword, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	user, err := s.Authenticate(ctx, token, zone)
	if err != nil {
		return err
	}
	now := s.now().UTC()
	if now.Sub(user.MFAVerifiedAt) > 5*time.Minute {
		return errors.New("recent MFA verification required")
	}
	id, err := hex.DecodeString(user.ID)
	if err != nil {
		return err
	}
	var currentHash string
	if err = s.db.QueryRowContext(ctx, "SELECT password_hash FROM user_accounts WHERE id=?", id).Scan(&currentHash); err != nil {
		return err
	}
	ok, err := VerifyPassword(currentPassword, currentHash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	newHash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET password_hash=?,password_changed_at=?,session_version=session_version+1 WHERE id=?", newHash, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", now, id); err != nil {
		return err
	}
	if err = auditIdentityTx(ctx, tx, id, "PASSWORD_CHANGED", "SUCCESS", map[string]any{}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) BeginPassword(ctx context.Context, username, password, zone string, browserBinding []byte, sourceIP string) (string, time.Time, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if err := s.checkRateLimit(ctx, "PASSWORD_ACCOUNT", username); err != nil {
		return "", time.Time{}, err
	}
	if err := s.checkRateLimit(ctx, "PASSWORD_IP", sourceIP); err != nil {
		return "", time.Time{}, err
	}
	var id []byte
	var hash, status string
	err := s.db.QueryRowContext(ctx, "SELECT id,password_hash,status FROM user_accounts WHERE username=?", username).Scan(&id, &hash, &status)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = VerifyPassword(password, dummyPasswordHash)
		_ = s.recordFailure(ctx, "PASSWORD_ACCOUNT", username, 10)
		_ = s.recordFailure(ctx, "PASSWORD_IP", sourceIP, 30)
		return "", time.Time{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", time.Time{}, err
	}
	ok, err := VerifyPassword(password, hash)
	if err != nil || !ok || status != "ACTIVE" {
		_ = s.recordFailure(ctx, "PASSWORD_ACCOUNT", username, 10)
		_ = s.recordFailure(ctx, "PASSWORD_IP", sourceIP, 30)
		return "", time.Time{}, ErrInvalidCredentials
	}
	raw, tokenHash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	challengeID, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().UTC().Add(5 * time.Minute)
	binding := sha256.Sum256(browserBinding)
	_, err = s.db.ExecContext(ctx, "INSERT INTO login_challenges(id,user_id,token_hash,portal_zone,browser_binding,expires_at) VALUES(?,?,?,?,?,?)", challengeID, id, tokenHash, zone, binding[:], expires)
	if err != nil {
		return "", time.Time{}, err
	}
	s.clearFailures(ctx, "PASSWORD_ACCOUNT", username)
	return raw, expires, nil
}

func (s *Service) CompleteTOTP(ctx context.Context, challenge, code, zone string, browserBinding []byte, sourceIP string) (Session, error) {
	if err := s.checkRateLimit(ctx, "MFA_IP", sourceIP); err != nil {
		return Session{}, err
	}
	tokenHash := sha256.Sum256([]byte(challenge))
	binding := sha256.Sum256(browserBinding)
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	var challengeID, userID, storedBinding, secretCipher []byte
	var expires time.Time
	var attempts int
	var consumed sql.NullTime
	var display, username, status string
	var lastStep sql.NullInt64
	var sessionVersion uint64
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.browser_binding,c.expires_at,c.attempts,c.consumed_at,u.display_name,u.username,u.status,u.mfa_secret_ciphertext,u.mfa_last_used_step,u.session_version FROM login_challenges c JOIN user_accounts u ON u.id=c.user_id WHERE c.token_hash=? AND c.portal_zone=? FOR UPDATE`, tokenHash[:], zone).Scan(&challengeID, &userID, &storedBinding, &expires, &attempts, &consumed, &display, &username, &status, &secretCipher, &lastStep, &sessionVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	if err = s.checkRateLimit(ctx, "MFA_ACCOUNT", username); err != nil {
		return Session{}, err
	}
	if consumed.Valid || !now.Before(expires) || attempts >= 5 {
		return Session{}, ErrChallengeExpired
	}
	if !equalBytes(storedBinding, binding[:]) || status != "ACTIVE" {
		return Session{}, ErrInvalidCredentials
	}
	secret, err := s.box.Open(secretCipher, "totp:"+hex.EncodeToString(userID))
	if err != nil {
		return Session{}, fmt.Errorf("decrypt TOTP secret: %w", err)
	}
	step, matchErr := MatchTOTP(secret, code, now, lastStep.Int64)
	if matchErr != nil {
		_, _ = tx.ExecContext(ctx, "UPDATE login_challenges SET attempts=attempts+1 WHERE id=?", challengeID)
		if err := tx.Commit(); err != nil {
			return Session{}, err
		}
		_ = s.recordFailure(ctx, "MFA_ACCOUNT", username, 10)
		_ = s.recordFailure(ctx, "MFA_IP", sourceIP, 30)
		return Session{}, ErrInvalidCredentials
	}
	res, err := tx.ExecContext(ctx, "UPDATE user_accounts SET mfa_last_used_step=? WHERE id=? AND (mfa_last_used_step IS NULL OR mfa_last_used_step<?)", step, userID, step)
	if err != nil {
		return Session{}, err
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		return Session{}, ErrTOTPReplay
	}
	rawSession, sessionHash, err := newToken()
	if err != nil {
		return Session{}, err
	}
	sessionID, err := randomID()
	if err != nil {
		return Session{}, err
	}
	sessionExpiry := now.Add(8 * time.Hour)
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,portal_zone,session_version,mfa_verified_at,last_seen_at,expires_at) VALUES(?,?,?,?,?,?,?,?)", sessionID, userID, sessionHash, zone, sessionVersion, now, now, sessionExpiry); err != nil {
		return Session{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE login_challenges SET consumed_at=? WHERE id=?", now, challengeID); err != nil {
		return Session{}, err
	}
	if err = auditIdentityTx(ctx, tx, userID, "LOGIN_MFA", "SUCCESS", map[string]any{"portal_zone": zone}, now); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	s.clearFailures(ctx, "MFA_ACCOUNT", username)
	return Session{Token: rawSession, ExpiresAt: sessionExpiry, UserID: hex.EncodeToString(userID), DisplayName: display}, nil
}

func randomID() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func newToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	raw := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	return raw, sum[:], nil
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// A fixed valid hash equalizes work for unknown users. It is not an account credential.
const dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=1$c29tZXNhbHQxMjM0NTY3OA$n2VtJq7bM2vQYf8PzRjVZ2WwDNtA1tTbXQUcYQ4B6oA"
