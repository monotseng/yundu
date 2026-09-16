package monitoring

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"time"
)

var (
	ErrDisabled     = errors.New("monitoring not configured")
	ErrUnauthorized = errors.New("invalid monitoring token")
	ErrForbidden    = errors.New("source is not allowed")
	ErrRateLimited  = errors.New("monitoring rate limited")
	ErrUnavailable  = errors.New("monitoring snapshot unavailable")
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}
type Token struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Enabled      bool       `json:"enabled"`
	AllowedCIDRs []string   `json:"allowed_cidrs"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
}
type CreatedToken struct {
	Token  Token  `json:"token"`
	Secret string `json:"secret"`
}
type Status struct {
	Enabled         bool       `json:"enabled"`
	ActiveTokens    uint64     `json:"active_tokens"`
	LastCollectedAt *time.Time `json:"last_collected_at,omitempty"`
	SnapshotValid   bool       `json:"snapshot_valid"`
}
type Snapshot struct {
	SchemaVersion                int    `json:"schema_version"`
	SnapshotValid                int    `json:"snapshot_valid"`
	SnapshotUpdatedAt            string `json:"snapshot_updated_at"`
	OpenPolicyBreachIncidents    uint64 `json:"open_policy_breach_incidents"`
	OpenIntegrityIncidents       uint64 `json:"open_integrity_incidents"`
	CrossZoneDenied5m            uint64 `json:"cross_zone_denied_5m"`
	UnauthorizedDownloadDenied5m uint64 `json:"unauthorized_download_denied_5m"`
	TextDenied5m                 uint64 `json:"text_denied_5m"`
	SizeDenied5m                 uint64 `json:"size_denied_5m"`
	QuotaDenied5m                uint64 `json:"quota_denied_5m"`
	ResourceBusy5m               uint64 `json:"resource_busy_5m"`
	MaxUserUploadAttempts5m      uint64 `json:"max_user_upload_attempts_5m"`
	MaxUserDownloadStarts5m      uint64 `json:"max_user_download_starts_5m"`
	MaxUserUploadBytes1h         uint64 `json:"max_user_upload_bytes_1h"`
	MaxUserDownloadBytes1h       uint64 `json:"max_user_download_bytes_1h"`
	ActiveUploads                uint64 `json:"active_uploads"`
	ActiveDownloads              uint64 `json:"active_downloads"`
	ActiveCopies                 uint64 `json:"active_copies"`
	AuditAggregationLagSeconds   uint64 `json:"audit_aggregation_lag_seconds"`
}

func New(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }
func (s *Service) CreateToken(ctx context.Context, name string, cidrs []string, expires *time.Time) (CreatedToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(cidrs) == 0 {
		return CreatedToken{}, errors.New("name and allowed CIDRs are required")
	}
	for _, v := range cidrs {
		if _, _, err := net.ParseCIDR(v); err != nil {
			return CreatedToken{}, errors.New("invalid allowed CIDR")
		}
	}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	digest := sha256.Sum256(raw)
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	encoded, _ := json.Marshal(cidrs)
	_, err := s.db.ExecContext(ctx, `INSERT INTO monitoring_tokens(id,name,token_hash,allowed_cidrs,expires_at) VALUES(?,?,?,?,?)`, id, name, digest[:], encoded, expires)
	if err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{Token: Token{ID: hex.EncodeToString(id), Name: name, Enabled: true, AllowedCIDRs: cidrs, ExpiresAt: expires}, Secret: base64.RawURLEncoding.EncodeToString(raw)}, nil
}
func (s *Service) ListTokens(ctx context.Context) ([]Token, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(id),name,enabled,allowed_cidrs,expires_at,last_used_at FROM monitoring_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Token{}
	for rows.Next() {
		var v Token
		var raw []byte
		var expires, last sql.NullTime
		if err = rows.Scan(&v.ID, &v.Name, &v.Enabled, &raw, &expires, &last); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		_ = json.Unmarshal(raw, &v.AllowedCIDRs)
		if expires.Valid {
			v.ExpiresAt = &expires.Time
		}
		if last.Valid {
			v.LastUsedAt = &last.Time
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Service) Revoke(ctx context.Context, idHex string) error {
	id, err := hex.DecodeString(idHex)
	if err != nil || len(id) != 16 {
		return ErrUnauthorized
	}
	res, err := s.db.ExecContext(ctx, `UPDATE monitoring_tokens SET enabled=FALSE,revoked_at=UTC_TIMESTAMP(6) WHERE id=? AND enabled=TRUE`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrUnauthorized
	}
	return nil
}
func (s *Service) Status(ctx context.Context) (Status, error) {
	var v Status
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitoring_tokens WHERE enabled=TRUE AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>UTC_TIMESTAMP(6))`).Scan(&v.ActiveTokens); err != nil {
		return v, err
	}
	v.Enabled = v.ActiveTokens > 0
	var updated sql.NullTime
	var valid sql.NullBool
	err := s.db.QueryRowContext(ctx, `SELECT updated_at,valid FROM monitoring_snapshots WHERE scope='global'`).Scan(&updated, &valid)
	if err != nil && err != sql.ErrNoRows {
		return v, err
	}
	if updated.Valid {
		v.LastCollectedAt = &updated.Time
	}
	v.SnapshotValid = valid.Valid && valid.Bool && updated.Valid && s.now().UTC().Sub(updated.Time) <= 2*time.Minute
	return v, nil
}
func (s *Service) Authorize(ctx context.Context, bearer, source string) error {
	var configured int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitoring_tokens WHERE enabled=TRUE AND revoked_at IS NULL`).Scan(&configured); err != nil {
		return err
	}
	if configured == 0 {
		return ErrDisabled
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(bearer))
	if err != nil || len(raw) != 32 {
		return ErrUnauthorized
	}
	digest := sha256.Sum256(raw)
	var id, allowed []byte
	var expires sql.NullTime
	err = s.db.QueryRowContext(ctx, `SELECT id,allowed_cidrs,expires_at FROM monitoring_tokens WHERE token_hash=? AND enabled=TRUE AND revoked_at IS NULL`, digest[:]).Scan(&id, &allowed, &expires)
	if err != nil {
		return ErrUnauthorized
	}
	now := s.now().UTC()
	if expires.Valid && !now.Before(expires.Time) {
		return ErrUnauthorized
	}
	ip := net.ParseIP(source)
	if ip == nil {
		return ErrForbidden
	}
	var cidrs []string
	_ = json.Unmarshal(allowed, &cidrs)
	ok := false
	for _, v := range cidrs {
		_, network, _ := net.ParseCIDR(v)
		if network != nil && network.Contains(ip) {
			ok = true
			break
		}
	}
	if !ok {
		return ErrForbidden
	}
	ipBytes := ip.To16()
	minute := now.Truncate(time.Minute)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO monitoring_access_buckets(token_id,source_ip,minute_start,count) VALUES(?,?,?,1) ON DUPLICATE KEY UPDATE count=count+1`, id, ipBytes, minute)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count FROM monitoring_access_buckets WHERE token_id=? AND source_ip=? AND minute_start=?`, id, ipBytes, minute).Scan(&count); err != nil {
		return err
	}
	if count > 12 {
		return ErrRateLimited
	}
	_, _ = tx.ExecContext(ctx, `UPDATE monitoring_tokens SET last_used_at=? WHERE id=?`, now, id)
	return tx.Commit()
}
func (s *Service) Aggregate(ctx context.Context) error {
	now := s.now().UTC()
	m := Snapshot{SchemaVersion: 1, SnapshotValid: 1, SnapshotUpdatedAt: now.Format(time.RFC3339)}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_incidents WHERE status<>'CLOSED' AND category='POLICY_BREACH'`).Scan(&m.OpenPolicyBreachIncidents); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_incidents WHERE status<>'CLOSED' AND category='INTEGRITY'`).Scan(&m.OpenIntegrityIncidents); err != nil {
		return err
	}
	queries := []struct {
		q string
		v *uint64
	}{
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='CROSS_ZONE_DENIED' AND occurred_at>?`, &m.CrossZoneDenied5m},
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='UNAUTHORIZED_DOWNLOAD_DENIED' AND occurred_at>?`, &m.UnauthorizedDownloadDenied5m},
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='TEXT_DENIED' AND occurred_at>?`, &m.TextDenied5m},
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='SIZE_DENIED' AND occurred_at>?`, &m.SizeDenied5m},
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='QUOTA_DENIED' AND occurred_at>?`, &m.QuotaDenied5m},
		{`SELECT COUNT(*) FROM monitoring_events WHERE event_type='RESOURCE_BUSY' AND occurred_at>?`, &m.ResourceBusy5m},
	}
	for _, x := range queries {
		if err := s.db.QueryRowContext(ctx, x.q, now.Add(-5*time.Minute)).Scan(x.v); err != nil {
			return err
		}
	}
	for kind, target := range map[string]*uint64{"UPLOAD": &m.ActiveUploads, "DOWNLOAD": &m.ActiveDownloads, "COPY": &m.ActiveCopies} {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM resource_slots WHERE kind=? AND holder_id IS NOT NULL AND lease_expires_at>?`, kind, now).Scan(target); err != nil {
			return err
		}
	}
	maxQueries := []struct {
		q      string
		since  time.Time
		target *uint64
	}{
		{`SELECT COALESCE(MAX(c),0) FROM (SELECT COUNT(*) c FROM upload_sessions WHERE created_at>? GROUP BY user_id) x`, now.Add(-5 * time.Minute), &m.MaxUserUploadAttempts5m},
		{`SELECT COALESCE(MAX(c),0) FROM (SELECT COUNT(*) c FROM download_deliveries WHERE started_at>? GROUP BY user_id) x`, now.Add(-5 * time.Minute), &m.MaxUserDownloadStarts5m},
		{`SELECT COALESCE(MAX(b),0) FROM (SELECT SUM(received_bytes) b FROM upload_sessions WHERE created_at>? GROUP BY user_id) x`, now.Add(-time.Hour), &m.MaxUserUploadBytes1h},
		{`SELECT COALESCE(MAX(b),0) FROM (SELECT SUM(bytes_sent) b FROM download_deliveries WHERE started_at>? GROUP BY user_id) x`, now.Add(-time.Hour), &m.MaxUserDownloadBytes1h},
	}
	for _, x := range maxQueries {
		if err := s.db.QueryRowContext(ctx, x.q, x.since).Scan(x.target); err != nil {
			return err
		}
	}
	// This aggregator reads the complete rolling windows in the same transactionally
	// persisted database, so a successful pass has no outstanding audit cursor lag.
	m.AuditAggregationLagSeconds = 0
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO monitoring_snapshots(scope,schema_version,window_end,valid,metrics,cursor_value,updated_at) VALUES('global',1,?,TRUE,?,0,?) ON DUPLICATE KEY UPDATE schema_version=1,window_end=VALUES(window_end),valid=TRUE,metrics=VALUES(metrics),updated_at=VALUES(updated_at)`, now, raw, now)
	return err
}
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	var raw []byte
	var valid bool
	var updated time.Time
	err := s.db.QueryRowContext(ctx, `SELECT metrics,valid,updated_at FROM monitoring_snapshots WHERE scope='global'`).Scan(&raw, &valid, &updated)
	if err != nil || !valid || s.now().UTC().Sub(updated) > 2*time.Minute {
		return Snapshot{}, ErrUnavailable
	}
	var m Snapshot
	if json.Unmarshal(raw, &m) != nil || m.SchemaVersion != 1 {
		return Snapshot{}, ErrUnavailable
	}
	return m, nil
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	_ = s.Aggregate(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Aggregate(ctx)
		}
	}
}
