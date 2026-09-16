package exchange

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound = errors.New("request not found")
	ErrConflict = errors.New("request version or state conflict")
	ErrQuota    = errors.New("request file quota exceeded")
)

type Limits struct {
	OfficeFileBytes, ProductionFileBytes, OfficeRequestBytes int64
	FilesPerRequest                                          int
}
type Service struct {
	db     *sql.DB
	limits Limits
	now    func() time.Time
}
type Request struct {
	ID               string    `json:"id"`
	RequestNo        string    `json:"request_no"`
	Direction        string    `json:"direction"`
	Classification   string    `json:"classification"`
	Purpose          string    `json:"purpose"`
	GroupID          string    `json:"group_id"`
	BusinessSystemID string    `json:"business_system_id,omitempty"`
	ExternalTicket   string    `json:"external_ticket,omitempty"`
	Status           string    `json:"status"`
	FileCount        int       `json:"file_count"`
	TotalBytes       string    `json:"total_bytes"`
	Version          uint64    `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
}
type UploadSession struct {
	ID               string    `json:"id"`
	FileID           string    `json:"file_id"`
	ObjectKey        string    `json:"object_key"`
	ExpectedBytes    int64     `json:"expected_bytes"`
	ExpiresAt        time.Time `json:"expires_at"`
	StorageVersionID string    `json:"storage_version_id"`
	Bucket           string    `json:"bucket"`
}

func (s *Service) RequestScope(ctx context.Context, userHex, requestHex string) (string, string, string, error) {
	user, err := decode(userHex)
	if err != nil {
		return "", "", "", err
	}
	request, err := decode(requestHex)
	if err != nil {
		return "", "", "", err
	}
	var group, business, direction string
	err = s.db.QueryRowContext(ctx, "SELECT LOWER(HEX(group_id)),COALESCE(LOWER(HEX(business_system_id)),''),direction FROM exchange_requests WHERE id=? AND requester_id=?", request, user).Scan(&group, &business, &direction)
	return group, business, direction, err
}
func (s *Service) Cancel(ctx context.Context, userHex, requestHex, reason string, expected uint64) error {
	if len([]rune(strings.TrimSpace(reason))) < 5 || len([]rune(reason)) > 500 {
		return errors.New("cancel reason must contain 5-500 characters")
	}
	user, err := decode(userHex)
	if err != nil {
		return err
	}
	request, err := decode(requestHex)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE exchange_requests SET status='CANCELLED',frozen_snapshot=JSON_SET(COALESCE(frozen_snapshot,JSON_OBJECT()),'$.cancellation_reason',?),version=version+1 WHERE id=? AND requester_id=? AND version=? AND status IN ('DRAFT','SCANNING','IN_REVIEW','WORKFLOW_BLOCKED','PENDING_APPROVAL')`, reason, request, user, expected)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Service) Submit(ctx context.Context, userHex, requestHex string, expectedVersion uint64, antivirus bool, workflowVersionHex, graphHash string) (uint64, error) {
	user, err := decode(userHex)
	if err != nil {
		return 0, err
	}
	request, err := decode(requestHex)
	if err != nil {
		return 0, err
	}
	workflowVersion, err := decode(workflowVersionHex)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var status string
	var version uint64
	var fileCount, ready int
	if err = tx.QueryRowContext(ctx, "SELECT status,version,file_count FROM exchange_requests WHERE id=? AND requester_id=? FOR UPDATE", request, user).Scan(&status, &version, &fileCount); err != nil {
		return 0, ErrNotFound
	}
	if status != "DRAFT" || version != expectedVersion {
		return 0, ErrConflict
	}
	if fileCount == 0 {
		return 0, errors.New("request has no files")
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_files WHERE request_id=? AND status='UPLOADED' AND source_version_id IS NOT NULL AND source_sha256 IS NOT NULL", request).Scan(&ready); err != nil {
		return 0, err
	}
	if ready != fileCount {
		return 0, errors.New("request contains incomplete files")
	}
	snapshot := fmt.Sprintf(`{"rule_version":"m4-source-v1","antivirus_required":%t,"file_count":%d,"workflow_graph_hash":"%s"}`, antivirus, fileCount, graphHash)
	if _, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='SCANNING',frozen_snapshot=?,workflow_version_id=?,version=version+1 WHERE id=?", snapshot, workflowVersion, request); err != nil {
		return 0, err
	}
	jobID, _ := newID()
	dedupe := "scan-source:" + requestHex + fmt.Sprintf(":%d", version+1)
	payload := fmt.Sprintf(`{"request_id":"%s"}`, requestHex)
	if _, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,type,dedupe_key,payload,status,available_at) VALUES(?,'SCAN_SOURCE',?,?,'PENDING',UTC_TIMESTAMP(6))", jobID, dedupe, payload); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return version + 1, nil
}

func New(db *sql.DB, limits Limits) *Service { return &Service{db: db, limits: limits, now: time.Now} }
func (s *Service) Create(ctx context.Context, userHex, zone, direction, groupHex, businessHex, classification, purpose, ticket string) (Request, error) {
	user, err := decode(userHex)
	if err != nil {
		return Request{}, err
	}
	group, err := decode(groupHex)
	if err != nil {
		return Request{}, err
	}
	direction = strings.ToUpper(direction)
	zone = strings.ToUpper(zone)
	if (direction == "OFFICE_TO_PROD" && zone != "OFFICE") || (direction == "PROD_TO_OFFICE" && zone != "PRODUCTION") {
		return Request{}, errors.New("direction does not match source portal")
	}
	if direction != "OFFICE_TO_PROD" && direction != "PROD_TO_OFFICE" {
		return Request{}, errors.New("invalid direction")
	}
	classification = strings.ToUpper(classification)
	if classification != "INTERNAL" && classification != "SENSITIVE" && classification != "HIGH" {
		return Request{}, errors.New("invalid classification")
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || len([]rune(purpose)) > 2000 {
		return Request{}, errors.New("invalid purpose")
	}
	var business any
	if businessHex != "" {
		business, err = decode(businessHex)
		if err != nil {
			return Request{}, err
		}
	}
	var member int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM group_memberships WHERE group_id=? AND user_id=? AND valid_from<=UTC_TIMESTAMP(6) AND (valid_until IS NULL OR valid_until>UTC_TIMESTAMP(6))", group, user).Scan(&member); err != nil {
		return Request{}, err
	}
	if member == 0 {
		return Request{}, errors.New("user is not an active group member")
	}
	if business != nil {
		var valid int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM business_systems b JOIN org_groups g ON g.id=? WHERE b.id=? AND b.status='ACTIVE' AND (b.department_id IS NULL OR b.department_id=g.department_id) AND JSON_CONTAINS(b.allowed_directions,JSON_QUOTE(?))`, group, business, direction).Scan(&valid); err != nil {
			return Request{}, err
		}
		if valid != 1 {
			return Request{}, errors.New("business system is not available to the selected team and direction")
		}
	}
	id, _ := newID()
	requestNo := fmt.Sprintf("YD%s-%s", s.now().UTC().Format("20060102"), strings.ToUpper(hex.EncodeToString(id[:4])))
	_, err = s.db.ExecContext(ctx, "INSERT INTO exchange_requests(id,request_no,requester_id,group_id,direction,classification,purpose,business_system_id,external_ticket,status) VALUES(?,?,?,?,?,?,?,?,?,'DRAFT')", id, requestNo, user, group, direction, classification, purpose, business, nullable(ticket))
	if err != nil {
		return Request{}, err
	}
	return Request{ID: hex.EncodeToString(id), RequestNo: requestNo, Direction: direction, Classification: classification, Purpose: purpose, GroupID: groupHex, BusinessSystemID: businessHex, ExternalTicket: ticket, Status: "DRAFT", TotalBytes: "0", Version: 1, CreatedAt: s.now().UTC()}, nil
}
func (s *Service) UpdateDraftPurpose(ctx context.Context, userHex, requestHex, purpose string, expected uint64) error {
	user, e := decode(userHex)
	if e != nil {
		return ErrNotFound
	}
	request, e := decode(requestHex)
	if e != nil {
		return ErrNotFound
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" || len([]rune(purpose)) > 2000 {
		return errors.New("invalid purpose")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, `UPDATE exchange_requests SET purpose=?,version=version+1 WHERE id=? AND requester_id=? AND status='DRAFT' AND version=?`, purpose, request, user, expected)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	details, _ := json.Marshal(map[string]any{"field": "purpose", "expected_version": expected, "source": "USER_EXPLICIT_EDIT"})
	eventID := make([]byte, 16)
	_, _ = rand.Read(eventID)
	digest := sha256.Sum256(append(eventID, details...))
	if _, e = tx.ExecContext(ctx, `INSERT INTO audit_events(event_id,occurred_at,actor_id,action,result,request_id,resource_type,resource_id,details,event_hash) VALUES(?,UTC_TIMESTAMP(6),?,'REQUEST_DRAFT_PURPOSE_UPDATED','SUCCESS',?,'REQUEST',?,?,?)`, eventID, user, request, request, details, digest[:]); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Service) ListOwn(ctx context.Context, userHex string) ([]Request, error) {
	user, err := decode(userHex)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(id),request_no,direction,classification,purpose,HEX(group_id),COALESCE(HEX(business_system_id),''),COALESCE(external_ticket,''),status,file_count,CAST(total_bytes AS CHAR),version,created_at FROM exchange_requests WHERE requester_id=? ORDER BY created_at DESC LIMIT 200`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		var v Request
		if err = rows.Scan(&v.ID, &v.RequestNo, &v.Direction, &v.Classification, &v.Purpose, &v.GroupID, &v.BusinessSystemID, &v.ExternalTicket, &v.Status, &v.FileCount, &v.TotalBytes, &v.Version, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.ID = strings.ToLower(v.ID)
		v.GroupID = strings.ToLower(v.GroupID)
		v.BusinessSystemID = strings.ToLower(v.BusinessSystemID)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) ReserveUpload(ctx context.Context, userHex, requestHex, zone, storageVersionHex, bucket, originalName, contentType string, size int64, expectedVersion uint64) (UploadSession, error) {
	user, err := decode(userHex)
	if err != nil {
		return UploadSession{}, err
	}
	requestID, err := decode(requestHex)
	if err != nil {
		return UploadSession{}, err
	}
	storageID, err := decode(storageVersionHex)
	if err != nil {
		return UploadSession{}, err
	}
	if err = ValidateFilename(originalName); err != nil {
		return UploadSession{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UploadSession{}, err
	}
	defer tx.Rollback()
	var direction, status string
	var version uint64
	var count int
	var total int64
	if err = tx.QueryRowContext(ctx, "SELECT direction,status,version,file_count,total_bytes FROM exchange_requests WHERE id=? AND requester_id=? FOR UPDATE", requestID, user).Scan(&direction, &status, &version, &count, &total); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UploadSession{}, ErrNotFound
		}
		return UploadSession{}, err
	}
	if status != "DRAFT" || version != expectedVersion {
		return UploadSession{}, ErrConflict
	}
	sourceZone := "PRODUCTION"
	maxFile := s.limits.ProductionFileBytes
	if direction == "OFFICE_TO_PROD" {
		sourceZone = "OFFICE"
		maxFile = s.limits.OfficeFileBytes
		ext := strings.ToLower(filepath.Ext(originalName))
		if ext != ".sql" && ext != ".csv" {
			return UploadSession{}, errors.New("office uploads only accept .sql or .csv")
		}
	}
	if strings.ToUpper(zone) != sourceZone {
		return UploadSession{}, errors.New("upload must use source portal")
	}
	if size <= 0 || size > maxFile || count+1 > s.limits.FilesPerRequest || total+size > s.limits.OfficeRequestBytes {
		return UploadSession{}, ErrQuota
	}
	fileID, _ := newID()
	sessionID, _ := newID()
	objectKey := "requests/" + requestHex + "/" + hex.EncodeToString(fileID)
	expires := s.now().UTC().Add(30 * time.Minute)
	if _, err = tx.ExecContext(ctx, "INSERT INTO request_files(id,request_id,original_name,object_key,source_storage_revision_id,source_bucket,size_bytes,status) VALUES(?,?,?,?,?,?,?,'RESERVED')", fileID, requestID, originalName, objectKey, storageID, bucket, size); err != nil {
		return UploadSession{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO upload_sessions(id,request_id,file_id,user_id,expected_bytes,expires_at) VALUES(?,?,?,?,?,?)", sessionID, requestID, fileID, user, size, expires); err != nil {
		return UploadSession{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET file_count=file_count+1,total_bytes=total_bytes+?,version=version+1 WHERE id=?", size, requestID); err != nil {
		return UploadSession{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO quota_counters(user_id,direction,period_start,reserved_bytes) VALUES(?,?,UTC_DATE(),?) ON DUPLICATE KEY UPDATE reserved_bytes=reserved_bytes+VALUES(reserved_bytes),version=version+1`, user, direction, size); err != nil {
		return UploadSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return UploadSession{}, err
	}
	_ = contentType
	return UploadSession{ID: hex.EncodeToString(sessionID), FileID: hex.EncodeToString(fileID), ObjectKey: objectKey, ExpectedBytes: size, ExpiresAt: expires, StorageVersionID: storageVersionHex, Bucket: bucket}, nil
}
func (s *Service) BeginUpload(ctx context.Context, userHex, sessionHex string) (UploadSession, error) {
	user, err := decode(userHex)
	if err != nil {
		return UploadSession{}, err
	}
	session, err := decode(sessionHex)
	if err != nil {
		return UploadSession{}, err
	}
	var v UploadSession
	var status string
	err = s.db.QueryRowContext(ctx, `SELECT HEX(u.file_id),f.object_key,u.expected_bytes,u.expires_at,HEX(f.source_storage_revision_id),f.source_bucket,u.status FROM upload_sessions u JOIN request_files f ON f.id=u.file_id WHERE u.id=? AND u.user_id=?`, session, user).Scan(&v.FileID, &v.ObjectKey, &v.ExpectedBytes, &v.ExpiresAt, &v.StorageVersionID, &v.Bucket, &status)
	if err != nil {
		return UploadSession{}, ErrNotFound
	}
	if status != "RESERVED" || s.now().UTC().After(v.ExpiresAt) {
		return UploadSession{}, ErrConflict
	}
	result, err := s.db.ExecContext(ctx, "UPDATE upload_sessions SET status='UPLOADING',version=version+1 WHERE id=? AND status='RESERVED'", session)
	if err != nil {
		return UploadSession{}, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return UploadSession{}, ErrConflict
	}
	v.ID = sessionHex
	v.FileID = strings.ToLower(v.FileID)
	v.StorageVersionID = strings.ToLower(v.StorageVersionID)
	return v, nil
}
func (s *Service) CompleteUpload(ctx context.Context, userHex, sessionHex, versionID, sha string, actual int64) error {
	return s.finish(ctx, userHex, sessionHex, "COMPLETED", versionID, sha, actual)
}
func (s *Service) FailUpload(ctx context.Context, userHex, sessionHex string) error {
	return s.finish(ctx, userHex, sessionHex, "FAILED", "", "", 0)
}
func (s *Service) AbortUpload(ctx context.Context, userHex, sessionHex string) error {
	user, err := decode(userHex)
	if err != nil {
		return err
	}
	session, err := decode(sessionHex)
	if err != nil {
		return err
	}
	var status string
	if err = s.db.QueryRowContext(ctx, "SELECT status FROM upload_sessions WHERE id=? AND user_id=?", session, user).Scan(&status); err != nil {
		return ErrNotFound
	}
	if status == "ABORTED" || status == "FAILED" {
		return nil
	}
	if status == "COMPLETED" {
		return ErrConflict
	}
	if status == "RESERVED" {
		result, err := s.db.ExecContext(ctx, "UPDATE upload_sessions SET status='UPLOADING',version=version+1 WHERE id=? AND status='RESERVED'", session)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
	}
	return s.finish(ctx, userHex, sessionHex, "ABORTED", "", "", 0)
}
func (s *Service) finish(ctx context.Context, userHex, sessionHex, status, versionID, sha string, actual int64) error {
	user, err := decode(userHex)
	if err != nil {
		return err
	}
	session, err := decode(sessionHex)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var file, request []byte
	var expected int64
	var direction string
	if err = tx.QueryRowContext(ctx, `SELECT u.file_id,u.request_id,u.expected_bytes,r.direction FROM upload_sessions u JOIN exchange_requests r ON r.id=u.request_id WHERE u.id=? AND u.user_id=? AND u.status='UPLOADING' FOR UPDATE`, session, user).Scan(&file, &request, &expected, &direction); err != nil {
		return err
	}
	if status == "COMPLETED" && actual != expected {
		return errors.New("uploaded size mismatch")
	}
	if status == "COMPLETED" {
		digest, err := hex.DecodeString(sha)
		if err != nil || len(digest) != 32 {
			return errors.New("invalid upload hash")
		}
		if versionID == "" {
			return errors.New("object version id missing")
		}
		_, err = tx.ExecContext(ctx, "UPDATE request_files SET source_version_id=?,source_sha256=?,status='UPLOADED',version=version+1 WHERE id=?", versionID, digest, file)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE quota_counters SET reserved_bytes=reserved_bytes-?,consumed_bytes=consumed_bytes+?,version=version+1 WHERE user_id=? AND direction=? AND period_start=UTC_DATE()", expected, expected, user, direction)
		if err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE request_files SET status='DELETED',version=version+1 WHERE id=?", file)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET file_count=file_count-1,total_bytes=total_bytes-?,version=version+1 WHERE id=?", expected, request)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE quota_counters SET reserved_bytes=reserved_bytes-?,version=version+1 WHERE user_id=? AND direction=? AND period_start=UTC_DATE()", expected, user, direction)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE upload_sessions SET status=?,version=version+1 WHERE id=?", status, session); err != nil {
		return err
	}
	return tx.Commit()
}
func newID() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func decode(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
func nullable(v string) any {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return v
}
func ValidateFilename(name string) error {
	if name == "" || name == "." || name == ".." || len([]rune(name)) > 200 || len([]byte(name)) > 240 || !utf8.ValidString(name) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("invalid filename")
	}
	if strings.ContainsAny(name, "/\\<>:\"|?*\r\n\x00") {
		return errors.New("unsafe filename")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return errors.New("unsafe filename control character")
		}
	}
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	reserved := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true, "LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}
	if reserved[base] {
		return errors.New("reserved filename")
	}
	return nil
}
