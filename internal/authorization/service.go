package authorization

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
)

var ErrDenied = errors.New("permission denied")

type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) Allowed(ctx context.Context, userHex, permission, scopeType, scopeHex string) (bool, error) {
	userID, err := hex.DecodeString(userHex)
	if err != nil || len(userID) != 16 {
		return false, ErrDenied
	}
	var scopeID []byte
	if scopeHex != "" {
		scopeID, err = hex.DecodeString(scopeHex)
		if err != nil || len(scopeID) != 16 {
			return false, ErrDenied
		}
	}
	var count int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_role_assignments a JOIN role_permissions p ON p.role_id=a.role_id WHERE a.user_id=? AND p.permission=? AND (a.scope_type='GLOBAL' OR (a.scope_type=? AND a.scope_id=?))`, userID, permission, scopeType, scopeID).Scan(&count)
	return count > 0, err
}
func (s *Service) Permissions(ctx context.Context, userHex string) ([]string, error) {
	userID, err := hex.DecodeString(userHex)
	if err != nil || len(userID) != 16 {
		return nil, ErrDenied
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT p.permission FROM user_role_assignments a JOIN role_permissions p ON p.role_id=a.role_id WHERE a.user_id=? ORDER BY p.permission`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var permission string
		if err = rows.Scan(&permission); err != nil {
			return nil, err
		}
		out = append(out, permission)
	}
	return out, rows.Err()
}
