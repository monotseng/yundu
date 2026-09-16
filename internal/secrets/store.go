package secrets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

type Store struct {
	db  *sql.DB
	box *Box
}

func NewStore(db *sql.DB, box *Box) *Store { return &Store{db: db, box: box} }
func (s *Store) Put(ctx context.Context, name, purpose string, value []byte, createdByHex string) (string, error) {
	if name == "" || purpose == "" || len(value) == 0 || len(value) > 3072 {
		return "", errors.New("invalid secret")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	createdBy, err := hex.DecodeString(createdByHex)
	if err != nil || len(createdBy) != 16 {
		return "", errors.New("invalid creator")
	}
	ciphertext, err := s.box.Seal(value, "secret:"+hex.EncodeToString(id)+":"+purpose)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO secret_records(id,name,purpose,ciphertext,created_by) VALUES(?,?,?,?,?)", id, name, purpose, ciphertext, createdBy)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}
func (s *Store) Get(ctx context.Context, idHex, expectedPurpose string) ([]byte, error) {
	id, err := hex.DecodeString(idHex)
	if err != nil || len(id) != 16 {
		return nil, errors.New("invalid secret reference")
	}
	var ciphertext []byte
	var purpose, status string
	err = s.db.QueryRowContext(ctx, "SELECT ciphertext,purpose,status FROM secret_records WHERE id=?", id).Scan(&ciphertext, &purpose, &status)
	if err != nil {
		return nil, err
	}
	if status != "ACTIVE" || purpose != expectedPurpose {
		return nil, errors.New("secret unavailable")
	}
	return s.box.Open(ciphertext, "secret:"+hex.EncodeToString(id)+":"+purpose)
}
func (s *Store) Retire(ctx context.Context, idHex string) error {
	id, err := hex.DecodeString(idHex)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE secret_records SET status='RETIRED',retired_at=UTC_TIMESTAMP(6) WHERE id=? AND status='ACTIVE'", id)
	return err
}

func (s *Store) SealEphemeral(value []byte, context string) ([]byte, error) {
	return s.box.Seal(value, "ephemeral:"+context)
}
func (s *Store) OpenEphemeral(value []byte, context string) ([]byte, error) {
	return s.box.Open(value, "ephemeral:"+context)
}
