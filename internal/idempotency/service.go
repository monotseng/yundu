package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var ErrConflict = errors.New("idempotency key conflict")
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

type Service struct{ db *sql.DB }
type Claim struct {
	Replay     bool
	StatusCode int
	Response   json.RawMessage
}

func New(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) Claim(ctx context.Context, actorHex, route, key string, payload []byte) (Claim, error) {
	if !keyPattern.MatchString(key) {
		return Claim{}, errors.New("invalid idempotency key")
	}
	actor, err := hex.DecodeString(actorHex)
	if err != nil || len(actor) != 16 {
		return Claim{}, errors.New("invalid actor")
	}
	digest := sha256.Sum256(payload)
	_, err = s.db.ExecContext(ctx, "INSERT INTO idempotency_records(actor_id,route,idempotency_key,payload_sha256,status_code,response_json,expires_at) VALUES(?,?,?,?,0,JSON_OBJECT(),?)", actor, route, key, digest[:], time.Now().UTC().Add(24*time.Hour))
	if err == nil {
		return Claim{}, nil
	}
	var stored []byte
	var status int
	var response []byte
	if scanErr := s.db.QueryRowContext(ctx, "SELECT payload_sha256,status_code,response_json FROM idempotency_records WHERE actor_id=? AND route=? AND idempotency_key=?", actor, route, key).Scan(&stored, &status, &response); scanErr != nil {
		return Claim{}, err
	}
	if string(stored) != string(digest[:]) || status == 0 {
		return Claim{}, ErrConflict
	}
	return Claim{Replay: true, StatusCode: status, Response: response}, nil
}
func (s *Service) Complete(ctx context.Context, actorHex, route, key string, status int, response []byte) error {
	actor, err := hex.DecodeString(actorHex)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE idempotency_records SET status_code=?,response_json=? WHERE actor_id=? AND route=? AND idempotency_key=? AND status_code=0", status, response, actor, route, key)
	return err
}
func (s *Service) Release(ctx context.Context, actorHex, route, key string) {
	actor, err := hex.DecodeString(actorHex)
	if err == nil {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM idempotency_records WHERE actor_id=? AND route=? AND idempotency_key=? AND status_code=0", actor, route, key)
	}
}
