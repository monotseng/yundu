package notifications

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrNotFound = errors.New("notification not found")

type Item struct {
	ID        string     `json:"id"`
	EventType string     `json:"event_type"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	RequestID *string    `json:"request_id,omitempty"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) List(ctx context.Context, userHex string, cursor time.Time, limit int) ([]Item, error) {
	user, err := hex.DecodeString(userHex)
	if err != nil || len(user) != 16 {
		return nil, ErrNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if cursor.IsZero() {
		cursor = time.Now().UTC().Add(time.Minute)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT LOWER(HEX(id)),event_type,title,body,LOWER(HEX(request_id)),read_at,created_at FROM inbox_notifications WHERE user_id=? AND created_at<? ORDER BY created_at DESC,id DESC LIMIT ?`, user, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		var v Item
		var req sql.NullString
		var read sql.NullTime
		if err := rows.Scan(&v.ID, &v.EventType, &v.Title, &v.Body, &req, &read, &v.CreatedAt); err != nil {
			return nil, err
		}
		if req.Valid {
			x := req.String
			v.RequestID = &x
		}
		if read.Valid {
			v.ReadAt = &read.Time
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
func (s *Service) Read(ctx context.Context, userHex, idHex string) error {
	user, e1 := hex.DecodeString(strings.TrimSpace(userHex))
	id, e2 := hex.DecodeString(strings.TrimSpace(idHex))
	if e1 != nil || e2 != nil || len(user) != 16 || len(id) != 16 {
		return ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE inbox_notifications SET read_at=COALESCE(read_at,UTC_TIMESTAMP(6)) WHERE id=? AND user_id=?`, id, user)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
