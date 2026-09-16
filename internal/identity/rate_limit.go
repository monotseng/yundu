package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"
)

var ErrRateLimited = errors.New("authentication attempts temporarily limited")

func (s *Service) checkRateLimit(ctx context.Context, scope, subject string) error {
	hash := sha256.Sum256([]byte(subject))
	var locked sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT locked_until FROM auth_rate_limits WHERE scope=? AND subject_hash=?", scope, hash[:]).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if locked.Valid && s.now().UTC().Before(locked.Time) {
		return ErrRateLimited
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, scope, subject string, limit int) error {
	hash := sha256.Sum256([]byte(subject))
	now := s.now().UTC()
	windowCutoff := now.Add(-15 * time.Minute)
	lockUntil := now.Add(15 * time.Minute)
	_, err := s.db.ExecContext(ctx, `INSERT INTO auth_rate_limits(scope,subject_hash,window_started_at,failure_count,locked_until) VALUES(?,?,?,1,NULL) ON DUPLICATE KEY UPDATE failure_count=IF(window_started_at<?,1,failure_count+1),window_started_at=IF(window_started_at<?,VALUES(window_started_at),window_started_at),locked_until=IF(IF(window_started_at<?,1,failure_count+1)>=?, ?,locked_until)`, scope, hash[:], now, windowCutoff, windowCutoff, windowCutoff, limit, lockUntil)
	return err
}

func (s *Service) clearFailures(ctx context.Context, scope, subject string) {
	hash := sha256.Sum256([]byte(subject))
	_, _ = s.db.ExecContext(ctx, "DELETE FROM auth_rate_limits WHERE scope=? AND subject_hash=?", scope, hash[:])
}
