package downloads

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/integrations"
	"yundu/internal/secrets"
)

var (
	ErrDenied      = errors.New("download is not authorized")
	ErrBusy        = errors.New("download capacity is busy")
	ErrConsumed    = errors.New("download grant is expired or already consumed")
	ErrIdempotency = errors.New("download idempotency conflict")
)

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)

type Service struct {
	db           *sql.DB
	integrations *integrations.Service
	secrets      *secrets.Store
	now          func() time.Time
}
type Grant struct {
	Token     string    `json:"grant"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Delivery struct {
	GrantID, RequestID, FileID, UserID, SessionID, StorageVersionID, Bucket, ObjectKey, VersionID, Filename, ExpectedHash string
	ExpectedBytes                                                                                                         int64
	Slot                                                                                                                  int
	Token                                                                                                                 uint64
	reader                                                                                                                io.ReadCloser
	ctx                                                                                                                   context.Context
	cancel                                                                                                                context.CancelFunc
}
type Receipt struct {
	ID         string     `json:"id"`
	Filename   string     `json:"filename"`
	Status     string     `json:"status"`
	BytesSent  int64      `json:"bytes_sent"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}
type AvailableRequest struct {
	ID        string           `json:"id"`
	RequestNo string           `json:"request_no"`
	Direction string           `json:"direction"`
	Status    string           `json:"status"`
	ExpiresAt time.Time        `json:"expires_at"`
	Version   uint64           `json:"version"`
	Files     []map[string]any `json:"files"`
}

func New(db *sql.DB, integrations *integrations.Service, secrets *secrets.Store) *Service {
	return &Service{db: db, integrations: integrations, secrets: secrets, now: time.Now}
}

func (s *Service) Issue(ctx context.Context, userHex, sessionToken, zone, fileHex, idempotencyKey string, expectedVersion uint64) (Grant, error) {
	if !idempotencyPattern.MatchString(idempotencyKey) {
		return Grant{}, ErrIdempotency
	}
	user, err := decode(userHex)
	if err != nil {
		return Grant{}, ErrDenied
	}
	file, err := decode(fileHex)
	if err != nil {
		return Grant{}, ErrDenied
	}
	sessionHash := sha256.Sum256([]byte(sessionToken))
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grant{}, err
	}
	defer tx.Rollback()
	var sessionID []byte
	if err = tx.QueryRowContext(ctx, `SELECT s.id FROM sessions s JOIN user_accounts u ON u.id=s.user_id WHERE s.token_hash=? AND s.user_id=? AND s.portal_zone=? AND s.revoked_at IS NULL AND s.expires_at>? AND s.session_version=u.session_version AND u.status='ACTIVE' FOR UPDATE`, sessionHash[:], user, zone, now).Scan(&sessionID); err != nil {
		return Grant{}, ErrDenied
	}
	var requestID, storageID, expectedHash []byte
	var requestVersion uint64
	var direction, status, bucket, objectKey, versionID string
	var expires time.Time
	var expectedBytes int64
	err = tx.QueryRowContext(ctx, `SELECT r.id,r.direction,r.status,r.version,r.expires_at,f.target_storage_revision_id,f.target_bucket,f.target_object_key,f.target_version_id,f.target_sha256,f.size_bytes FROM request_files f JOIN exchange_requests r ON r.id=f.request_id WHERE f.id=? AND r.requester_id=? AND f.status='READY' FOR UPDATE`, file, user).Scan(&requestID, &direction, &status, &requestVersion, &expires, &storageID, &bucket, &objectKey, &versionID, &expectedHash, &expectedBytes)
	if err != nil || requestVersion != expectedVersion || !downloadable(direction, zone, status, expires, now) {
		return Grant{}, ErrDenied
	}
	payloadHash := sha256.Sum256([]byte(fmt.Sprintf("%d", expectedVersion)))
	var existingID, existingSession, storedPayload, ciphertext []byte
	var existingExpiry time.Time
	err = tx.QueryRowContext(ctx, `SELECT id,session_id,idempotency_payload_sha256,grant_token_ciphertext,expires_at FROM download_grants WHERE user_id=? AND file_id=? AND idempotency_key=? FOR UPDATE`, user, file, idempotencyKey).Scan(&existingID, &existingSession, &storedPayload, &ciphertext, &existingExpiry)
	if err == nil {
		if !bytesEqual(existingSession, sessionID) || !bytesEqual(storedPayload, payloadHash[:]) {
			return Grant{}, ErrIdempotency
		}
		plain, openErr := s.secrets.OpenEphemeral(ciphertext, "download-grant:"+hex.EncodeToString(existingID))
		if openErr != nil {
			return Grant{}, openErr
		}
		return Grant{Token: base64.RawURLEncoding.EncodeToString(plain), ExpiresAt: existingExpiry}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Grant{}, err
	}
	if err = s.verifyWholeRequestTx(ctx, tx, requestID); err != nil {
		return Grant{}, ErrDenied
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_grants WHERE user_id=? AND status IN ('ISSUED','STARTED') AND (status='STARTED' OR expires_at>?)`, user, now).Scan(&active); err != nil {
		return Grant{}, err
	}
	if active >= 2 {
		return Grant{}, ErrBusy
	}
	var slot int
	var slotToken uint64
	err = tx.QueryRowContext(ctx, `SELECT slot_no,fencing_token FROM resource_slots WHERE kind='DOWNLOAD' AND (holder_id IS NULL OR lease_expires_at<?) ORDER BY slot_no LIMIT 1 FOR UPDATE SKIP LOCKED`, now).Scan(&slot, &slotToken)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrBusy
	}
	if err != nil {
		return Grant{}, err
	}
	grantID := random(16)
	rawToken := random(32)
	sealedToken, err := s.secrets.SealEphemeral(rawToken, "download-grant:"+hex.EncodeToString(grantID))
	if err != nil {
		return Grant{}, err
	}
	tokenHash := sha256.Sum256(rawToken)
	grantExpires := now.Add(60 * time.Second)
	fencing := slotToken + 1
	if _, err = tx.ExecContext(ctx, `UPDATE resource_slots SET holder_id=?,fencing_token=?,lease_expires_at=? WHERE kind='DOWNLOAD' AND slot_no=?`, grantID, fencing, grantExpires, slot); err != nil {
		return Grant{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO download_grants(id,request_id,file_id,user_id,session_id,portal_zone,download_slot_no,target_storage_revision_id,target_bucket,target_object_key,target_version_id,expected_sha256,expected_bytes,token_hash,status,fencing_token,lease_expires_at,expires_at,idempotency_key,idempotency_payload_sha256,grant_token_ciphertext) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'ISSUED',?,?,?,?,?,?)`, grantID, requestID, file, user, sessionID, zone, slot, storageID, bucket, objectKey, versionID, expectedHash, expectedBytes, tokenHash[:], fencing, grantExpires, grantExpires, idempotencyKey, payloadHash[:], sealedToken)
	if err != nil {
		return Grant{}, err
	}
	if err = auditTx(ctx, tx, user, "DOWNLOAD_GRANTED", "SUCCESS", requestID, file, map[string]any{"grant_id": hex.EncodeToString(grantID), "expires_at": grantExpires, "portal_zone": zone}); err != nil {
		return Grant{}, err
	}
	if err = tx.Commit(); err != nil {
		return Grant{}, err
	}
	return Grant{Token: base64.RawURLEncoding.EncodeToString(rawToken), ExpiresAt: grantExpires}, nil
}

func (s *Service) Start(ctx context.Context, userHex, sessionToken, zone, grantToken string) (Delivery, error) {
	user, err := decode(userHex)
	if err != nil {
		return Delivery{}, ErrDenied
	}
	raw, err := base64.RawURLEncoding.DecodeString(grantToken)
	if err != nil || len(raw) != 32 {
		return Delivery{}, ErrConsumed
	}
	grantHash := sha256.Sum256(raw)
	sessionHash := sha256.Sum256([]byte(sessionToken))
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Delivery{}, err
	}
	defer tx.Rollback()
	var d Delivery
	var grantID, requestID, fileID, storedUser, sessionID, storageID, expectedHash []byte
	var status, requestStatus, direction string
	var requestExpires, grantExpires time.Time
	err = tx.QueryRowContext(ctx, `SELECT g.id,g.request_id,g.file_id,g.user_id,g.session_id,g.download_slot_no,g.target_storage_revision_id,g.target_bucket,g.target_object_key,g.target_version_id,g.expected_sha256,g.expected_bytes,g.fencing_token,g.expires_at,g.status,r.status,r.direction,r.expires_at,f.original_name FROM download_grants g JOIN exchange_requests r ON r.id=g.request_id JOIN request_files f ON f.id=g.file_id WHERE g.token_hash=? FOR UPDATE`, grantHash[:]).Scan(&grantID, &requestID, &fileID, &storedUser, &sessionID, &d.Slot, &storageID, &d.Bucket, &d.ObjectKey, &d.VersionID, &expectedHash, &d.ExpectedBytes, &d.Token, &grantExpires, &status, &requestStatus, &direction, &requestExpires, &d.Filename)
	if err != nil || status != "ISSUED" || !bytesEqual(user, storedUser) || !now.Before(grantExpires) || !downloadable(direction, zone, requestStatus, requestExpires, now) {
		return Delivery{}, ErrConsumed
	}
	var liveSession []byte
	if err = tx.QueryRowContext(ctx, `SELECT s.id FROM sessions s JOIN user_accounts u ON u.id=s.user_id WHERE s.token_hash=? AND s.id=? AND s.user_id=? AND s.portal_zone=? AND s.revoked_at IS NULL AND s.expires_at>? AND s.session_version=u.session_version AND u.status='ACTIVE'`, sessionHash[:], sessionID, user, zone, now).Scan(&liveSession); err != nil {
		return Delivery{}, ErrDenied
	}
	if err = s.verifyWholeRequestTx(ctx, tx, requestID); err != nil {
		return Delivery{}, ErrDenied
	}
	lease := now.Add(90 * time.Second)
	res, err := tx.ExecContext(ctx, `UPDATE download_grants SET status='STARTED',started_at=?,lease_expires_at=?,last_checked_at=?,version=version+1 WHERE id=? AND status='ISSUED' AND fencing_token=?`, now, lease, now, grantID, d.Token)
	if err != nil {
		return Delivery{}, err
	}
	rows, _ := res.RowsAffected()
	if rows != 1 {
		return Delivery{}, ErrConsumed
	}
	if _, err = tx.ExecContext(ctx, `UPDATE resource_slots SET lease_expires_at=? WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, lease, d.Slot, grantID, d.Token); err != nil {
		return Delivery{}, err
	}
	deliveryID := random(16)
	if _, err = tx.ExecContext(ctx, `INSERT INTO download_deliveries(id,grant_id,request_id,file_id,user_id,status) VALUES(?,?,?,?,?,'STARTED')`, deliveryID, grantID, requestID, fileID, user); err != nil {
		return Delivery{}, err
	}
	if err = auditTx(ctx, tx, user, "DOWNLOAD_STARTED", "SUCCESS", requestID, fileID, map[string]any{"grant_id": hex.EncodeToString(grantID)}); err != nil {
		return Delivery{}, err
	}
	if err = tx.Commit(); err != nil {
		return Delivery{}, err
	}
	d.GrantID, d.RequestID, d.FileID, d.UserID, d.SessionID, d.StorageVersionID, d.ExpectedHash = hex.EncodeToString(grantID), hex.EncodeToString(requestID), hex.EncodeToString(fileID), userHex, hex.EncodeToString(sessionID), hex.EncodeToString(storageID), hex.EncodeToString(expectedHash)
	version, err := s.integrations.GetUsableStorageVersion(ctx, d.StorageVersionID)
	if err != nil {
		s.interrupt(context.Background(), d, 0, "STORAGE_UNAVAILABLE")
		return Delivery{}, err
	}
	access, err := s.secrets.Get(ctx, version.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		s.interrupt(context.Background(), d, 0, "CREDENTIAL_UNAVAILABLE")
		return Delivery{}, err
	}
	secret, err := s.secrets.Get(ctx, version.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		clear(access)
		s.interrupt(context.Background(), d, 0, "CREDENTIAL_UNAVAILABLE")
		return Delivery{}, err
	}
	reader, size, err := s3adapter.OpenVersion(ctx, version.Config, d.ObjectKey, d.VersionID, string(access), string(secret))
	clear(access)
	clear(secret)
	if err != nil || size != d.ExpectedBytes {
		if reader != nil {
			reader.Close()
		}
		s.interrupt(context.Background(), d, 0, "TARGET_OPEN_FAILED")
		if err == nil {
			err = errors.New("target size mismatch")
		}
		return Delivery{}, err
	}
	d.ctx, d.cancel = context.WithCancel(ctx)
	d.reader = reader
	return d, nil
}

func (s *Service) Stream(d Delivery, w io.Writer) (int64, error) {
	defer d.reader.Close()
	defer d.cancel()
	monitorDone := make(chan error, 1)
	go s.monitor(d, monitorDone)
	hash := sha256.New()
	buf := make([]byte, 256*1024)
	var sent int64
	var streamErr error
	for {
		n, readErr := d.reader.Read(buf)
		if n > 0 {
			written, writeErr := w.Write(buf[:n])
			if written > 0 {
				_, _ = hash.Write(buf[:written])
				sent += int64(written)
			}
			if writeErr != nil || written != n {
				streamErr = io.ErrUnexpectedEOF
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			streamErr = readErr
			break
		}
	}
	d.cancel()
	monitorErr := <-monitorDone
	if streamErr == nil && monitorErr != nil {
		streamErr = monitorErr
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if streamErr == nil && (sent != d.ExpectedBytes || digest != d.ExpectedHash) {
		streamErr = errors.New("download integrity check failed")
	}
	if streamErr != nil {
		s.interrupt(context.Background(), d, sent, "DOWNLOAD_INTERRUPTED")
		return sent, streamErr
	}
	if err := s.complete(context.Background(), d, sent, digest); err != nil {
		return sent, err
	}
	return sent, nil
}

func (s *Service) monitor(d Delivery, done chan<- error) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			if err := s.renew(d.ctx, d); err != nil {
				d.cancel()
				done <- err
				return
			}
		}
	}
}
func (s *Service) renew(ctx context.Context, d Delivery) error {
	grant, _ := hex.DecodeString(d.GrantID)
	request, _ := hex.DecodeString(d.RequestID)
	session, _ := hex.DecodeString(d.SessionID)
	user, _ := hex.DecodeString(d.UserID)
	storage, _ := hex.DecodeString(d.StorageVersionID)
	now := s.now().UTC()
	lease := now.Add(90 * time.Second)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM download_grants g JOIN exchange_requests r ON r.id=g.request_id JOIN sessions s ON s.id=g.session_id JOIN user_accounts u ON u.id=g.user_id JOIN integration_versions iv ON iv.id=g.target_storage_revision_id JOIN integration_definitions idef ON idef.id=iv.integration_id WHERE g.id=? AND g.status='STARTED' AND g.fencing_token=? AND g.request_id=? AND g.session_id=? AND g.user_id=? AND g.target_storage_revision_id=? AND r.requester_id=g.user_id AND r.status IN ('READY','PARTIALLY_DELIVERED','DELIVERED') AND r.expires_at>? AND s.revoked_at IS NULL AND s.expires_at>? AND s.last_seen_at>? AND s.session_version=u.session_version AND u.status='ACTIVE' AND idef.status='PUBLISHED' AND iv.status='PUBLISHED'`, grant, d.Token, request, session, user, storage, now, now, now.Add(-30*time.Minute)).Scan(&valid)
	if err != nil || valid != 1 {
		return ErrDenied
	}
	res, err := tx.ExecContext(ctx, `UPDATE download_grants SET lease_expires_at=?,last_checked_at=? WHERE id=? AND status='STARTED' AND fencing_token=?`, lease, now, grant, d.Token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConsumed
	}
	res, err = tx.ExecContext(ctx, `UPDATE resource_slots SET lease_expires_at=? WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, lease, d.Slot, grant, d.Token)
	if err != nil {
		return err
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return ErrConsumed
	}
	return tx.Commit()
}

func (s *Service) complete(ctx context.Context, d Delivery, sent int64, digestHex string) error {
	grant, _ := hex.DecodeString(d.GrantID)
	request, _ := hex.DecodeString(d.RequestID)
	file, _ := hex.DecodeString(d.FileID)
	digest, _ := hex.DecodeString(digestHex)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE download_grants SET status='COMPLETED',bytes_sent=?,completed_at=UTC_TIMESTAMP(6),lease_expires_at=NULL,version=version+1 WHERE id=? AND status='STARTED' AND fencing_token=?`, sent, grant, d.Token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConsumed
	}
	_, err = tx.ExecContext(ctx, `UPDATE download_deliveries SET status='COMPLETED',bytes_sent=?,computed_sha256=?,finished_at=UTC_TIMESTAMP(6) WHERE grant_id=? AND status='STARTED'`, sent, digest, grant)
	if err != nil {
		return err
	}
	user, _ := hex.DecodeString(d.UserID)
	if err = auditTx(ctx, tx, user, "DOWNLOAD_DELIVERY_COMPLETED", "SUCCESS", request, file, map[string]any{"grant_id": d.GrantID, "bytes_sent": sent}); err != nil {
		return err
	}
	_, _ = tx.ExecContext(ctx, `UPDATE request_files SET first_delivered_at=COALESCE(first_delivered_at,UTC_TIMESTAMP(6)) WHERE id=?`, file)
	_, _ = tx.ExecContext(ctx, `UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, d.Slot, grant, d.Token)
	var missing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_files f WHERE f.request_id=? AND NOT EXISTS(SELECT 1 FROM download_deliveries dd WHERE dd.file_id=f.id AND dd.status='COMPLETED')`, request).Scan(&missing); err != nil {
		return err
	}
	if missing == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE exchange_requests SET status='DELIVERED',all_files_delivered_at=COALESCE(all_files_delivered_at,UTC_TIMESTAMP(6)),version=version+1 WHERE id=? AND status IN ('READY','PARTIALLY_DELIVERED','DELIVERED')`, request)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE exchange_requests SET status='PARTIALLY_DELIVERED',version=version+1 WHERE id=? AND status='READY'`, request)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) interrupt(ctx context.Context, d Delivery, sent int64, code string) {
	grant, _ := hex.DecodeString(d.GrantID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	res, _ := tx.ExecContext(ctx, `UPDATE download_grants SET status='INTERRUPTED',bytes_sent=?,error_code=?,lease_expires_at=NULL,version=version+1 WHERE id=? AND status IN ('STARTED','REVOKED') AND fencing_token=?`, sent, code, grant, d.Token)
	n, _ := res.RowsAffected()
	if n != 1 {
		return
	}
	_, _ = tx.ExecContext(ctx, `UPDATE download_deliveries SET status='INTERRUPTED',bytes_sent=?,error_code=?,finished_at=UTC_TIMESTAMP(6) WHERE grant_id=? AND status='STARTED'`, sent, code, grant)
	request, _ := hex.DecodeString(d.RequestID)
	file, _ := hex.DecodeString(d.FileID)
	user, _ := hex.DecodeString(d.UserID)
	if auditTx(ctx, tx, user, "DOWNLOAD_INTERRUPTED", "FAILED", request, file, map[string]any{"grant_id": d.GrantID, "bytes_sent": sent, "error_code": code}) != nil {
		return
	}
	_, _ = tx.ExecContext(ctx, `UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, d.Slot, grant, d.Token)
	_ = tx.Commit()
}

func (s *Service) RevokeRequest(ctx context.Context, actorHex, requestHex, reason string, expected uint64) error {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 5 || len([]rune(reason)) > 500 {
		return errors.New("revoke reason must contain 5-500 characters")
	}
	request, err := decode(requestHex)
	if err != nil {
		return ErrDenied
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var version uint64
	if err = tx.QueryRowContext(ctx, `SELECT status,version FROM exchange_requests WHERE id=? FOR UPDATE`, request).Scan(&status, &version); err != nil {
		return ErrDenied
	}
	if version != expected || (status != "READY" && status != "PARTIALLY_DELIVERED" && status != "DELIVERED") {
		return ErrDenied
	}
	type activeGrant struct {
		id     []byte
		slot   int
		token  uint64
		status string
	}
	var grants []activeGrant
	rows, err := tx.QueryContext(ctx, `SELECT id,download_slot_no,fencing_token,status FROM download_grants WHERE request_id=? AND status IN ('ISSUED','STARTED') FOR UPDATE`, request)
	if err != nil {
		return err
	}
	for rows.Next() {
		var g activeGrant
		if err = rows.Scan(&g.id, &g.slot, &g.token, &g.status); err != nil {
			rows.Close()
			return err
		}
		grants = append(grants, g)
	}
	rows.Close()
	if _, err = tx.ExecContext(ctx, `UPDATE exchange_requests SET status='REVOKED',frozen_snapshot=JSON_SET(COALESCE(frozen_snapshot,JSON_OBJECT()),'$.revocation_reason',?),version=version+1 WHERE id=? AND version=?`, reason, request, expected); err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return ErrDenied
	}
	if err = auditTx(ctx, tx, actor, "REQUEST_REVOKED", "SUCCESS", request, nil, map[string]any{"reason": reason}); err != nil {
		return err
	}
	for _, g := range grants {
		_, _ = tx.ExecContext(ctx, `UPDATE download_grants SET status='REVOKED',error_code='REQUEST_REVOKED',version=version+1 WHERE id=?`, g.id)
		if g.status == "ISSUED" {
			_, _ = tx.ExecContext(ctx, `UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, g.slot, g.id, g.token)
		}
	}
	return tx.Commit()
}

func (s *Service) verifyWholeRequestTx(ctx context.Context, tx *sql.Tx, request []byte) error {
	var invalid int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM request_files f WHERE f.request_id=? AND (f.status<>'READY' OR f.target_version_id IS NULL OR f.target_object_key IS NULL OR f.target_sha256 IS NULL OR f.source_sha256<>f.target_sha256 OR EXISTS(SELECT 1 FROM file_check_results c WHERE c.file_id=f.id AND c.stage='TARGET' AND c.required=TRUE AND c.status<>'PASSED'))`, request).Scan(&invalid)
	if err != nil {
		return err
	}
	if invalid != 0 {
		return ErrDenied
	}
	return nil
}
func (s *Service) ListReceipts(ctx context.Context, userHex, requestHex string) ([]Receipt, error) {
	user, err := decode(userHex)
	if err != nil {
		return nil, ErrDenied
	}
	request, err := decode(requestHex)
	if err != nil {
		return nil, ErrDenied
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(dd.id),f.original_name,dd.status,dd.bytes_sent,dd.finished_at FROM download_deliveries dd JOIN request_files f ON f.id=dd.file_id JOIN exchange_requests r ON r.id=dd.request_id WHERE dd.request_id=? AND r.requester_id=? AND dd.user_id=? ORDER BY dd.started_at DESC`, request, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Receipt{}
	for rows.Next() {
		var v Receipt
		var finished sql.NullTime
		if err = rows.Scan(&v.ID, &v.Filename, &v.Status, &v.BytesSent, &finished); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		if finished.Valid {
			v.FinishedAt = &finished.Time
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Service) ListAvailable(ctx context.Context, userHex, zone string) ([]AvailableRequest, error) {
	user, err := decode(userHex)
	if err != nil {
		return nil, ErrDenied
	}
	now := s.now().UTC()
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(r.id),r.request_no,r.direction,r.status,r.expires_at,r.version,HEX(f.id),f.original_name,f.size_bytes,LOWER(HEX(f.target_sha256)),f.first_delivered_at FROM exchange_requests r JOIN request_files f ON f.request_id=r.id WHERE r.requester_id=? AND r.status IN ('READY','PARTIALLY_DELIVERED','DELIVERED') AND r.expires_at>? AND ((r.direction='PROD_TO_OFFICE' AND ?='OFFICE') OR (r.direction='OFFICE_TO_PROD' AND ?='PRODUCTION')) AND f.status='READY' ORDER BY r.published_at DESC,f.created_at`, user, now, zone, zone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AvailableRequest{}
	index := map[string]int{}
	for rows.Next() {
		var requestID, requestNo, direction, status, fileID, name, size, hash string
		var expires time.Time
		var version uint64
		var delivered sql.NullTime
		if err = rows.Scan(&requestID, &requestNo, &direction, &status, &expires, &version, &fileID, &name, &size, &hash, &delivered); err != nil {
			return nil, err
		}
		requestID = strings.ToLower(requestID)
		at, ok := index[requestID]
		if !ok {
			at = len(items)
			index[requestID] = at
			items = append(items, AvailableRequest{ID: requestID, RequestNo: requestNo, Direction: direction, Status: status, ExpiresAt: expires, Version: version, Files: []map[string]any{}})
		}
		items[at].Files = append(items[at].Files, map[string]any{"id": strings.ToLower(fileID), "filename": name, "size_bytes": size, "sha256": hash, "delivered": delivered.Valid})
	}
	return items, rows.Err()
}
func (s *Service) Expire(ctx context.Context) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,download_slot_no,fencing_token,status FROM download_grants WHERE (status='ISSUED' AND expires_at<=?) OR (status='STARTED' AND lease_expires_at<=?) FOR UPDATE SKIP LOCKED`, now, now)
	if err != nil {
		return
	}
	type expired struct {
		id     []byte
		slot   int
		token  uint64
		status string
	}
	var items []expired
	for rows.Next() {
		var v expired
		if rows.Scan(&v.id, &v.slot, &v.token, &v.status) == nil {
			items = append(items, v)
		}
	}
	rows.Close()
	for _, v := range items {
		next := "EXPIRED"
		if v.status == "STARTED" {
			next = "INTERRUPTED"
		}
		_, _ = tx.ExecContext(ctx, `UPDATE download_grants SET status=?,error_code='LEASE_OR_START_EXPIRED',lease_expires_at=NULL,version=version+1 WHERE id=? AND fencing_token=?`, next, v.id, v.token)
		if v.status == "STARTED" {
			_, _ = tx.ExecContext(ctx, `UPDATE download_deliveries SET status='INTERRUPTED',error_code='LEASE_EXPIRED',finished_at=UTC_TIMESTAMP(6) WHERE grant_id=? AND status='STARTED'`, v.id)
		}
		_, _ = tx.ExecContext(ctx, `UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='DOWNLOAD' AND slot_no=? AND holder_id=? AND fencing_token=?`, v.slot, v.id, v.token)
	}
	_, _ = tx.ExecContext(ctx, `UPDATE exchange_requests SET status='EXPIRED',version=version+1 WHERE status IN ('READY','PARTIALLY_DELIVERED','DELIVERED') AND expires_at<=?`, now)
	_, _ = tx.ExecContext(ctx, `INSERT IGNORE INTO jobs(id,type,dedupe_key,payload,status,available_at) SELECT UUID_TO_BIN(UUID()),'DELETE_OBJECT_VERSION',CONCAT('delete-source:',LOWER(HEX(f.id))),JSON_OBJECT('file_id',LOWER(HEX(f.id)),'location','SOURCE','storage_version_id',LOWER(HEX(f.source_storage_revision_id)),'object_key',f.object_key,'version_id',f.source_version_id),'PENDING',UTC_TIMESTAMP(6) FROM request_files f JOIN exchange_requests r ON r.id=f.request_id WHERE r.status='EXPIRED' AND f.source_deleted_at IS NULL AND f.source_storage_revision_id IS NOT NULL AND f.source_version_id IS NOT NULL`)
	_, _ = tx.ExecContext(ctx, `INSERT IGNORE INTO jobs(id,type,dedupe_key,payload,status,available_at) SELECT UUID_TO_BIN(UUID()),'DELETE_OBJECT_VERSION',CONCAT('delete-target:',LOWER(HEX(f.id))),JSON_OBJECT('file_id',LOWER(HEX(f.id)),'location','TARGET','storage_version_id',LOWER(HEX(f.target_storage_revision_id)),'object_key',f.target_object_key,'version_id',f.target_version_id),'PENDING',UTC_TIMESTAMP(6) FROM request_files f JOIN exchange_requests r ON r.id=f.request_id WHERE r.status='EXPIRED' AND f.target_deleted_at IS NULL AND f.target_storage_revision_id IS NOT NULL AND f.target_version_id IS NOT NULL`)
	_ = tx.Commit()
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.Expire(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Expire(ctx)
			s.cleanupOne(ctx)
		}
	}
}

type deletion struct {
	FileID           string `json:"file_id"`
	Location         string `json:"location"`
	StorageVersionID string `json:"storage_version_id"`
	ObjectKey        string `json:"object_key"`
	VersionID        string `json:"version_id"`
}

func (s *Service) cleanupOne(ctx context.Context) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var jobID, raw []byte
	var token uint64
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT id,payload,lease_token,attempts FROM jobs WHERE type='DELETE_OBJECT_VERSION' AND ((status='PENDING' AND available_at<=UTC_TIMESTAMP(6)) OR (status='RUNNING' AND lease_expires_at<UTC_TIMESTAMP(6))) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&jobID, &raw, &token, &attempts)
	if err != nil {
		return
	}
	token++
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET status='RUNNING',lease_holder='m7-lifecycle',lease_token=?,lease_expires_at=UTC_TIMESTAMP(6)+INTERVAL 90 SECOND,attempts=attempts+1 WHERE id=?`, token, jobID)
	if err != nil {
		return
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return
	}
	if tx.Commit() != nil {
		return
	}
	var task deletion
	if json.Unmarshal(raw, &task) != nil {
		s.failDeletion(ctx, jobID, token, attempts+1, "INVALID_PAYLOAD")
		return
	}
	version, err := s.integrations.GetUsableStorageVersion(ctx, task.StorageVersionID)
	if err != nil {
		s.failDeletion(ctx, jobID, token, attempts+1, "STORAGE_UNAVAILABLE")
		return
	}
	access, err := s.secrets.Get(ctx, version.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		s.failDeletion(ctx, jobID, token, attempts+1, "CREDENTIAL_UNAVAILABLE")
		return
	}
	secret, err := s.secrets.Get(ctx, version.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		clear(access)
		s.failDeletion(ctx, jobID, token, attempts+1, "CREDENTIAL_UNAVAILABLE")
		return
	}
	err = s3adapter.DeleteVersion(ctx, version.Config, task.ObjectKey, task.VersionID, string(access), string(secret))
	clear(access)
	clear(secret)
	if err != nil {
		s.failDeletion(ctx, jobID, token, attempts+1, "DELETE_FAILED")
		return
	}
	fileID, err := decode(task.FileID)
	if err != nil {
		s.failDeletion(ctx, jobID, token, attempts+1, "INVALID_FILE")
		return
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	res, err = tx.ExecContext(ctx, `UPDATE jobs SET status='SUCCEEDED',lease_holder=NULL,lease_expires_at=NULL WHERE id=? AND status='RUNNING' AND lease_token=?`, jobID, token)
	if err != nil {
		return
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		return
	}
	column := "source_deleted_at"
	if task.Location == "TARGET" {
		column = "target_deleted_at"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE request_files SET `+column+`=UTC_TIMESTAMP(6) WHERE id=?`, fileID); err != nil {
		return
	}
	_, _ = tx.ExecContext(ctx, `UPDATE request_files SET status='DELETED' WHERE id=? AND source_deleted_at IS NOT NULL AND target_deleted_at IS NOT NULL`, fileID)
	_ = tx.Commit()
}
func (s *Service) failDeletion(ctx context.Context, id []byte, token uint64, attempt int, code string) {
	status := "PENDING"
	if attempt >= 5 {
		status = "DEAD"
	}
	delay := time.Duration(1<<minInt(attempt-1, 4)) * time.Minute
	_, _ = s.db.ExecContext(ctx, `UPDATE jobs SET status=?,available_at=?,lease_holder=NULL,lease_expires_at=NULL,last_error_code=? WHERE id=? AND status='RUNNING' AND lease_token=?`, status, s.now().UTC().Add(delay), code, id, token)
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func downloadable(direction, zone, status string, expires, now time.Time) bool {
	target := "OFFICE"
	if direction == "OFFICE_TO_PROD" {
		target = "PRODUCTION"
	}
	return zone == target && (status == "READY" || status == "PARTIALLY_DELIVERED" || status == "DELIVERED") && now.Before(expires)
}
func decode(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, fmt.Errorf("invalid id")
	}
	return b, nil
}
func random(n int) []byte         { b := make([]byte, n); _, _ = rand.Read(b); return b }
func bytesEqual(a, b []byte) bool { return len(a) == len(b) && string(a) == string(b) }

func auditTx(ctx context.Context, tx *sql.Tx, actor []byte, action, result string, request, file []byte, details map[string]any) error {
	eventID := random(16)
	now := time.Now().UTC()
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	digest := sha256.New()
	digest.Write(eventID)
	digest.Write([]byte(action))
	digest.Write([]byte(result))
	digest.Write([]byte(now.Format(time.RFC3339Nano)))
	digest.Write(payload)
	resource := request
	if len(file) == 16 {
		resource = file
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,occurred_at,actor_id,action,result,request_id,resource_type,resource_id,details,event_hash) VALUES(?,?,?,?,?,?,'DOWNLOAD',?,?,?)`, eventID, now, actor, action, result, request, resource, payload, digest.Sum(nil))
	return err
}
