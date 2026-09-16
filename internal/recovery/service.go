package recovery

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type State struct {
	Frozen                     bool       `json:"frozen"`
	AuthenticationBlockedUntil *time.Time `json:"authentication_blocked_until,omitempty"`
	Reason                     string     `json:"reason"`
	Version                    uint64     `json:"version"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) State(ctx context.Context) (State, error) {
	var v State
	var until sql.NullTime
	var reason sql.NullString
	e := s.db.QueryRowContext(ctx, `SELECT recovery_freeze,authentication_blocked_until,reason,version,updated_at FROM system_guard WHERE id=1`).Scan(&v.Frozen, &until, &reason, &v.Version, &v.UpdatedAt)
	if until.Valid {
		v.AuthenticationBlockedUntil = &until.Time
	}
	if reason.Valid {
		v.Reason = reason.String
	}
	return v, e
}
func (s *Service) Enter(ctx context.Context, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("recovery reason is required")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, e = tx.ExecContext(ctx, `UPDATE system_guard SET recovery_freeze=TRUE,authentication_blocked_until=?,reason=?,version=version+1,updated_by=NULL WHERE id=1`, now.Add(2*time.Minute), reason); e != nil {
		return e
	}
	timed := []string{`UPDATE sessions SET revoked_at=? WHERE revoked_at IS NULL`, `UPDATE login_challenges SET consumed_at=? WHERE consumed_at IS NULL`, `UPDATE activation_tokens SET consumed_at=? WHERE consumed_at IS NULL`}
	for _, q := range timed {
		if _, e = tx.ExecContext(ctx, q, now); e != nil {
			return e
		}
	}
	plain := []string{`UPDATE download_grants SET status='REVOKED' WHERE status IN ('ISSUED','STARTED')`, `UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL,fencing_token=fencing_token+1 WHERE holder_id IS NOT NULL`}
	for _, q := range plain {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	return audit(ctx, tx, nil, "RECOVERY_MODE_ENTERED", reason, now)
}
func (s *Service) Release(ctx context.Context, userHex, reason string, expected uint64, mfaAt time.Time) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("release reason is required")
	}
	now := time.Now().UTC()
	if mfaAt.IsZero() || now.Sub(mfaAt) > 5*time.Minute {
		return errors.New("recent MFA required")
	}
	user, e := hex.DecodeString(userHex)
	if e != nil || len(user) != 16 {
		return errors.New("invalid actor")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE system_guard SET recovery_freeze=FALSE,authentication_blocked_until=NULL,reason=?,version=version+1,updated_by=? WHERE id=1 AND recovery_freeze=TRUE AND version=?`, reason, user, expected)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("guard version conflict")
	}
	return audit(ctx, tx, user, "RECOVERY_MODE_RELEASED", reason, now)
}
func audit(ctx context.Context, tx *sql.Tx, actor []byte, action, reason string, now time.Time) error {
	details, _ := json.Marshal(map[string]any{"reason": reason})
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	hash := sha256.Sum256(append(id, details...))
	if _, e := tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,occurred_at,actor_id,action,result,resource_type,details,event_hash) VALUES(?,?,? ,?,'SUCCESS','SYSTEM_GUARD',?,?)`, id, now, actor, action, details, hash[:]); e != nil {
		return e
	}
	return tx.Commit()
}
