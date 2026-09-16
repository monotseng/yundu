package audit

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/integrations"
	"yundu/internal/secrets"
)

var ErrInvalidRange = errors.New("a UTC time window of at most 31 days is required")

type Filter struct {
	From, To           time.Time
	Cursor             uint64
	Limit              int
	ActorID, RequestID string
	Action, Result     string
	Direction          string
}

type Event struct {
	ID            uint64          `json:"id"`
	EventID       string          `json:"event_id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorID       *string         `json:"actor_id,omitempty"`
	ActorName     *string         `json:"actor_name,omitempty"`
	ActorUsername *string         `json:"actor_username,omitempty"`
	ActorAvatar   *string         `json:"actor_avatar,omitempty"`
	Action        string          `json:"action"`
	Result        string          `json:"result"`
	RequestID     *string         `json:"request_id,omitempty"`
	ResourceType  *string         `json:"resource_type,omitempty"`
	ResourceID    *string         `json:"resource_id,omitempty"`
	PortalZone    *string         `json:"portal_zone,omitempty"`
	TraceID       *string         `json:"trace_id,omitempty"`
	Direction     *string         `json:"direction,omitempty"`
	Details       json.RawMessage `json:"details"`
	EventHash     string          `json:"event_hash"`
}
type Page struct {
	Items      []Event `json:"items"`
	NextCursor uint64  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.From.Before(f.To) || f.To.Sub(f.From) > 31*24*time.Hour {
		return Page{}, ErrInvalidRange
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	q := `SELECT a.id,LOWER(HEX(a.event_id)),a.occurred_at,LOWER(HEX(a.actor_id)),u.display_name,u.username,u.avatar_emoji,a.action,a.result,LOWER(HEX(a.request_id)),a.resource_type,LOWER(HEX(a.resource_id)),a.portal_zone,a.trace_id,r.direction,a.details,LOWER(HEX(a.event_hash)) FROM audit_events a LEFT JOIN user_accounts u ON u.id=a.actor_id LEFT JOIN exchange_requests r ON r.id=a.request_id WHERE a.occurred_at>=? AND a.occurred_at<? AND a.id>?`
	args := []any{f.From.UTC(), f.To.UTC(), f.Cursor}
	addHex := func(column, value string) {
		if strings.TrimSpace(value) != "" {
			q += " AND " + column + "=UNHEX(?)"
			args = append(args, strings.TrimSpace(value))
		}
	}
	addText := func(column, value string) {
		if strings.TrimSpace(value) != "" {
			q += " AND " + column + "=?"
			args = append(args, strings.TrimSpace(value))
		}
	}
	addHex("a.actor_id", f.ActorID)
	addHex("a.request_id", f.RequestID)
	addText("a.action", f.Action)
	addText("a.result", f.Result)
	addText("r.direction", f.Direction)
	q += ` ORDER BY a.id LIMIT ?`
	args = append(args, f.Limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Items: []Event{}}
	for rows.Next() {
		var v Event
		var actor, actorName, actorUsername, actorAvatar, request, resource, resourceType, zone, trace, direction sql.NullString
		if err := rows.Scan(&v.ID, &v.EventID, &v.OccurredAt, &actor, &actorName, &actorUsername, &actorAvatar, &v.Action, &v.Result, &request, &resourceType, &resource, &zone, &trace, &direction, &v.Details, &v.EventHash); err != nil {
			return Page{}, err
		}
		set := func(n sql.NullString) *string {
			if !n.Valid {
				return nil
			}
			x := n.String
			return &x
		}
		v.ActorID = set(actor)
		v.ActorName = set(actorName)
		v.ActorUsername = set(actorUsername)
		v.ActorAvatar = set(actorAvatar)
		v.RequestID = set(request)
		v.ResourceType = set(resourceType)
		v.ResourceID = set(resource)
		v.PortalZone = set(zone)
		v.TraceID = set(trace)
		v.Direction = set(direction)
		page.Items = append(page.Items, v)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Items) > f.Limit {
		page.Items = page.Items[:f.Limit]
		page.HasMore = true
	}
	if len(page.Items) > 0 {
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

type Archive struct {
	ID         string    `json:"id"`
	RangeStart time.Time `json:"range_start"`
	RangeEnd   time.Time `json:"range_end"`
	EventCount uint64    `json:"event_count"`
	SHA256     string    `json:"sha256"`
	Signature  string    `json:"signature"`
	Bucket     string    `json:"bucket"`
	ObjectKey  string    `json:"object_key"`
	VersionID  string    `json:"version_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Service) ListArchives(ctx context.Context) ([]Archive, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT LOWER(HEX(id)),range_start,range_end,event_count,LOWER(HEX(sha256)),LOWER(HEX(signature)),bucket,object_key,version_id,status,created_at FROM audit_archives ORDER BY range_start DESC LIMIT 400`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Archive{}
	for rows.Next() {
		var v Archive
		if e = rows.Scan(&v.ID, &v.RangeStart, &v.RangeEnd, &v.EventCount, &v.SHA256, &v.Signature, &v.Bucket, &v.ObjectKey, &v.VersionID, &v.Status, &v.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Archive(ctx context.Context, from, to time.Time, storage integrations.Version, store *secrets.Store, signingKeyID string) (Archive, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.Sub(from) > 24*time.Hour {
		return Archive{}, errors.New("archive range must be at most one day")
	}
	signingKey, e := store.Get(ctx, signingKeyID, "AUDIT_SIGNING_KEY")
	if e != nil {
		return Archive{}, errors.New("audit signing key unavailable")
	}
	access, e := store.Get(ctx, storage.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if e != nil {
		return Archive{}, e
	}
	secret, e := store.Get(ctx, storage.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if e != nil {
		return Archive{}, e
	}
	tmp, e := os.CreateTemp("", "yundu-audit-*.jsonl")
	if e != nil {
		return Archive{}, e
	}
	name := tmp.Name()
	defer os.Remove(name)
	hash := sha256.New()
	writer := bufio.NewWriter(io.MultiWriter(tmp, hash))
	count := uint64(0)
	cursor := uint64(0)
	for {
		page, e := s.List(ctx, Filter{From: from, To: to, Cursor: cursor, Limit: 200})
		if e != nil {
			tmp.Close()
			return Archive{}, e
		}
		for _, event := range page.Items {
			line, e := json.Marshal(event)
			if e != nil {
				tmp.Close()
				return Archive{}, e
			}
			if _, e = writer.Write(append(line, '\n')); e != nil {
				tmp.Close()
				return Archive{}, e
			}
			count++
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if count == 0 {
		tmp.Close()
		return Archive{}, errors.New("archive window has no events")
	}
	if e = writer.Flush(); e != nil {
		tmp.Close()
		return Archive{}, e
	}
	size, e := tmp.Seek(0, io.SeekCurrent)
	if e != nil {
		tmp.Close()
		return Archive{}, e
	}
	digest := hash.Sum(nil)
	manifest := fmt.Sprintf("%s|%s|%d|%s", from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), count, hex.EncodeToString(digest))
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write([]byte(manifest))
	signature := mac.Sum(nil)
	if _, e = tmp.Seek(0, io.SeekStart); e != nil {
		tmp.Close()
		return Archive{}, e
	}
	key := strings.Trim(storage.Config.Prefix, "/")
	if key != "" {
		key += "/"
	}
	key += "audit-archives/" + from.UTC().Format("2006/01/02") + ".jsonl"
	uploaded, e := s3adapter.Upload(ctx, storage.Config, key, "application/x-ndjson", tmp, size, string(access), string(secret))
	closeErr := tmp.Close()
	if e != nil {
		return Archive{}, e
	}
	if closeErr != nil {
		return Archive{}, closeErr
	}
	if uploaded.SHA256 != hex.EncodeToString(digest) || uploaded.VersionID == "" {
		return Archive{}, errors.New("archive upload integrity or version check failed")
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	storageID, e := hex.DecodeString(storage.ID)
	if e != nil {
		return Archive{}, e
	}
	signingID, e := hex.DecodeString(signingKeyID)
	if e != nil {
		return Archive{}, e
	}
	now := time.Now().UTC()
	_, e = s.db.ExecContext(ctx, `INSERT INTO audit_archives(id,range_start,range_end,event_count,sha256,signature,signing_key_id,storage_version_id,bucket,object_key,version_id,status,verified_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,'VERIFIED',?)`, id, from.UTC(), to.UTC(), count, digest, signature, signingID, storageID, storage.Config.Bucket, key, uploaded.VersionID, now)
	if e != nil {
		return Archive{}, e
	}
	return Archive{ID: hex.EncodeToString(id), RangeStart: from.UTC(), RangeEnd: to.UTC(), EventCount: count, SHA256: hex.EncodeToString(digest), Signature: hex.EncodeToString(signature), Bucket: storage.Config.Bucket, ObjectKey: key, VersionID: uploaded.VersionID, Status: "VERIFIED", CreatedAt: now}, nil
}

// Run performs the daily archive after an operator has established the independent
// storage and signing-key binding with the first verified archive.
func (s *Service) Run(ctx context.Context, integrationService *integrations.Service, store *secrets.Store) {
	run := func() {
		now := time.Now().UTC()
		to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		from := to.Add(-24 * time.Hour)
		var storageID, signingID string
		err := s.db.QueryRowContext(ctx, `SELECT LOWER(HEX(storage_version_id)),LOWER(HEX(signing_key_id)) FROM audit_archives WHERE status='VERIFIED' ORDER BY created_at DESC LIMIT 1`).Scan(&storageID, &signingID)
		if err != nil {
			return
		}
		var exists int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_archives WHERE range_start=? AND range_end=? AND status='VERIFIED'`, from, to).Scan(&exists) != nil || exists > 0 {
			return
		}
		storage, err := integrationService.GetUsableStorageVersion(ctx, storageID)
		if err != nil {
			return
		}
		_, _ = s.Archive(ctx, from, to, storage, store, signingID)
	}
	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
