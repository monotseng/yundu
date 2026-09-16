package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type RecoveryRequest struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) InitiateRecovery(ctx context.Context, actorToken, zone, targetUserID, note string) (RecoveryRequest, error) {
	actor, err := s.Authenticate(ctx, actorToken, zone)
	if err != nil {
		return RecoveryRequest{}, err
	}
	actorID, _ := hex.DecodeString(actor.ID)
	targetID, err := hex.DecodeString(targetUserID)
	if err != nil || len(targetID) != 16 {
		return RecoveryRequest{}, errors.New("invalid target user")
	}
	if equalBytes(actorID, targetID) {
		return RecoveryRequest{}, errors.New("self recovery is forbidden")
	}
	if len([]rune(strings.TrimSpace(note))) < 10 || len([]rune(note)) > 1000 {
		return RecoveryRequest{}, errors.New("identity verification note must contain 10-1000 characters")
	}
	allowed, err := s.hasRole(ctx, actorID, "ORG_ADMIN")
	if err != nil {
		return RecoveryRequest{}, err
	}
	if !allowed {
		return RecoveryRequest{}, errors.New("organization administrator permission required")
	}
	id, err := randomID()
	if err != nil {
		return RecoveryRequest{}, err
	}
	expires := s.now().UTC().Add(24 * time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryRequest{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "INSERT INTO mfa_recovery_requests(id,user_id,initiated_by,status,identity_verification_note,expires_at) SELECT ?,id,?,'PENDING_REVIEW',?,? FROM user_accounts WHERE id=? AND status='ACTIVE'", id, actorID, note, expires, targetID)
	if err != nil {
		return RecoveryRequest{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return RecoveryRequest{}, errors.New("target user is not active")
	}
	if err = auditIdentityTx(ctx, tx, actorID, "MFA_RECOVERY_INITIATED", "SUCCESS", map[string]any{"target_user_id": targetUserID, "recovery_id": hex.EncodeToString(id)}, s.now().UTC()); err != nil {
		return RecoveryRequest{}, err
	}
	if err = tx.Commit(); err != nil {
		return RecoveryRequest{}, err
	}
	return RecoveryRequest{ID: hex.EncodeToString(id), ExpiresAt: expires}, nil
}

func (s *Service) ApproveRecovery(ctx context.Context, actorToken, zone, recoveryID, note string, expectedVersion uint64) (string, time.Time, error) {
	actor, err := s.Authenticate(ctx, actorToken, zone)
	if err != nil {
		return "", time.Time{}, err
	}
	actorID, _ := hex.DecodeString(actor.ID)
	if s.now().UTC().Sub(actor.MFAVerifiedAt) > 5*time.Minute {
		return "", time.Time{}, errors.New("recent MFA verification required")
	}
	allowed, err := s.hasRole(ctx, actorID, "SECURITY_ADMIN")
	if err != nil {
		return "", time.Time{}, err
	}
	if !allowed {
		return "", time.Time{}, errors.New("security administrator permission required")
	}
	id, err := hex.DecodeString(recoveryID)
	if err != nil || len(id) != 16 {
		return "", time.Time{}, errors.New("invalid recovery request")
	}
	if len([]rune(strings.TrimSpace(note))) < 10 || len([]rune(note)) > 1000 {
		return "", time.Time{}, errors.New("review note must contain 10-1000 characters")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()
	var targetID, initiatorID []byte
	var status string
	var version uint64
	var expires time.Time
	err = tx.QueryRowContext(ctx, "SELECT user_id,initiated_by,status,version,expires_at FROM mfa_recovery_requests WHERE id=? FOR UPDATE", id).Scan(&targetID, &initiatorID, &status, &version, &expires)
	if err != nil {
		return "", time.Time{}, err
	}
	if status != "PENDING_REVIEW" || version != expectedVersion || !now.Before(expires) {
		return "", time.Time{}, errors.New("recovery request state conflict")
	}
	if equalBytes(actorID, initiatorID) || equalBytes(actorID, targetID) {
		return "", time.Time{}, errors.New("recovery requires an independent reviewer")
	}
	tokenID, err := randomID()
	if err != nil {
		return "", time.Time{}, err
	}
	raw, tokenHash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	tokenExpiry := now.Add(30 * time.Minute)
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET status='MFA_REBIND_REQUIRED',mfa_state='RESET_PENDING',mfa_secret_ciphertext=NULL,mfa_last_used_step=NULL,session_version=session_version+1 WHERE id=?", targetID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", now, targetID); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO activation_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'MFA_REBIND',?)", tokenID, targetID, tokenHash, tokenExpiry); err != nil {
		return "", time.Time{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE mfa_recovery_requests SET status='APPROVED',reviewed_by=?,review_note=?,reviewed_at=?,version=version+1 WHERE id=?", actorID, note, now, id); err != nil {
		return "", time.Time{}, err
	}
	if err = auditIdentityTx(ctx, tx, actorID, "MFA_RECOVERY_APPROVED", "SUCCESS", map[string]any{"target_user_id": hex.EncodeToString(targetID), "recovery_id": recoveryID}, now); err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return raw, tokenExpiry, nil
}

func (s *Service) BeginRebind(ctx context.Context, rebindToken, password string) (Binding, error) {
	hash := sha256.Sum256([]byte(rebindToken))
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	var tokenID, userID []byte
	var passwordHash, status, username string
	var expires time.Time
	var consumed sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT a.id,a.user_id,a.expires_at,a.consumed_at,u.password_hash,u.status,u.username FROM activation_tokens a JOIN user_accounts u ON u.id=a.user_id WHERE a.token_hash=? AND a.purpose='MFA_REBIND' FOR UPDATE`, hash[:]).Scan(&tokenID, &userID, &expires, &consumed, &passwordHash, &status, &username)
	if err != nil || consumed.Valid || !now.Before(expires) || status != "MFA_REBIND_REQUIRED" {
		return Binding{}, ErrInvalidCredentials
	}
	ok, err := VerifyPassword(password, passwordHash)
	if err != nil || !ok {
		return Binding{}, ErrInvalidCredentials
	}
	secret, err := NewTOTPSecret()
	if err != nil {
		return Binding{}, err
	}
	ciphertext, err := s.box.Seal(secret, "totp:"+hex.EncodeToString(userID))
	if err != nil {
		return Binding{}, err
	}
	bindingID, err := randomID()
	if err != nil {
		return Binding{}, err
	}
	raw, bindingHash, err := newToken()
	if err != nil {
		return Binding{}, err
	}
	bindingExpiry := now.Add(10 * time.Minute)
	if _, err = tx.ExecContext(ctx, "UPDATE user_accounts SET status='MFA_BIND_REQUIRED',mfa_state='PENDING',mfa_secret_ciphertext=? WHERE id=?", ciphertext, userID); err != nil {
		return Binding{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE activation_tokens SET consumed_at=? WHERE id=?", now, tokenID); err != nil {
		return Binding{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO mfa_binding_tokens(id,user_id,token_hash,purpose,expires_at) VALUES(?,?,?,'REBIND',?)", bindingID, userID, bindingHash, bindingExpiry); err != nil {
		return Binding{}, err
	}
	if err = auditIdentityTx(ctx, tx, userID, "MFA_REBIND_STARTED", "SUCCESS", map[string]any{}, now); err != nil {
		return Binding{}, err
	}
	if err = tx.Commit(); err != nil {
		return Binding{}, err
	}
	return Binding{Token: raw, Secret: EncodeTOTPSecret(secret), URI: TOTPURI(username, secret), ExpiresAt: bindingExpiry}, nil
}

func (s *Service) hasRole(ctx context.Context, userID []byte, code string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_role_assignments a JOIN roles r ON r.id=a.role_id WHERE a.user_id=? AND r.code=?", userID, code).Scan(&count)
	return count > 0, err
}
