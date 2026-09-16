package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"time"
)

func auditIdentityTx(ctx context.Context, tx *sql.Tx, actorID []byte, action, result string, details map[string]any, now time.Time) error {
	eventID, err := randomID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	digest := sha256.New()
	digest.Write(eventID)
	digest.Write([]byte(action))
	digest.Write([]byte(result))
	digest.Write([]byte(now.UTC().Format(time.RFC3339Nano)))
	digest.Write(payload)
	_, err = tx.ExecContext(ctx, "INSERT INTO audit_events(event_id,occurred_at,actor_id,action,result,resource_type,resource_id,details,event_hash) VALUES(?,?,?,?,?,'IDENTITY',?,?,?)", eventID, now, nullableID(actorID), action, result, nullableID(actorID), payload, digest.Sum(nil))
	return err
}

func nullableID(id []byte) any {
	if len(id) == 16 {
		return id
	}
	return nil
}
