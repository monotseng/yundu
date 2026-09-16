package incidents

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

var (
	ErrNotFound           = errors.New("incident not found")
	ErrConflict           = errors.New("incident version conflict")
	ErrResolutionRequired = errors.New("resolution required")
	ErrRecentMFARequired  = errors.New("recent MFA required")
)

type Item struct {
	ID             string     `json:"id"`
	Category       string     `json:"category"`
	Severity       string     `json:"severity"`
	Status         string     `json:"status"`
	FirstSeenAt    time.Time  `json:"first_seen_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	RequestID      *string    `json:"request_id,omitempty"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	Resolution     *string    `json:"resolution,omitempty"`
	Version        uint64     `json:"version"`
}
type Service struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }
func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT LOWER(HEX(id)),category,severity,status,first_seen_at,last_seen_at,LOWER(HEX(request_id)),acknowledged_at,closed_at,resolution,version FROM security_incidents ORDER BY FIELD(status,'OPEN','ACKNOWLEDGED','CLOSED'),last_seen_at DESC LIMIT 200`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var v Item
		var request, resolution sql.NullString
		var ack, closed sql.NullTime
		if e = rows.Scan(&v.ID, &v.Category, &v.Severity, &v.Status, &v.FirstSeenAt, &v.LastSeenAt, &request, &ack, &closed, &resolution, &v.Version); e != nil {
			return nil, e
		}
		if request.Valid {
			x := request.String
			v.RequestID = &x
		}
		if resolution.Valid {
			x := resolution.String
			v.Resolution = &x
		}
		if ack.Valid {
			x := ack.Time
			v.AcknowledgedAt = &x
		}
		if closed.Valid {
			x := closed.Time
			v.ClosedAt = &x
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Service) Acknowledge(ctx context.Context, idHex, userHex string, version uint64) error {
	return s.mutate(ctx, idHex, userHex, "ACKNOWLEDGED", "", version)
}
func (s *Service) Close(ctx context.Context, idHex, userHex, resolution string, version uint64, mfaAt time.Time) error {
	if strings.TrimSpace(resolution) == "" {
		return ErrResolutionRequired
	}
	now := s.now().UTC()
	if mfaAt.IsZero() || now.Sub(mfaAt) > 5*time.Minute || mfaAt.After(now.Add(time.Minute)) {
		return ErrRecentMFARequired
	}
	return s.mutate(ctx, idHex, userHex, "CLOSED", strings.TrimSpace(resolution), version)
}
func (s *Service) mutate(ctx context.Context, idHex, userHex, status, resolution string, version uint64) error {
	id, e1 := hex.DecodeString(idHex)
	user, e2 := hex.DecodeString(userHex)
	if e1 != nil || e2 != nil || len(id) != 16 || len(user) != 16 {
		return ErrNotFound
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var result sql.Result
	if status == "ACKNOWLEDGED" {
		result, e = tx.ExecContext(ctx, `UPDATE security_incidents SET status='ACKNOWLEDGED',acknowledged_by=?,acknowledged_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=? AND status='OPEN' AND version=?`, user, id, version)
	} else {
		result, e = tx.ExecContext(ctx, `UPDATE security_incidents SET status='CLOSED',closed_by=?,closed_at=UTC_TIMESTAMP(6),resolution=?,version=version+1 WHERE id=? AND status<>'CLOSED' AND version=?`, user, resolution, id, version)
	}
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	details, _ := json.Marshal(map[string]any{"incident_id": idHex, "status": status, "resolution": resolution, "expected_version": version})
	eventID := make([]byte, 16)
	_, _ = rand.Read(eventID)
	digest := sha256.Sum256(append(eventID, details...))
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,occurred_at,actor_id,action,result,resource_type,resource_id,details,event_hash) VALUES(?,UTC_TIMESTAMP(6),?,?,'SUCCESS','SECURITY_INCIDENT',?,?,?)`, eventID, user, "SECURITY_INCIDENT_"+status, id, details, digest[:]); e != nil {
		return e
	}
	return tx.Commit()
}
