package transfer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"yundu/internal/adapters/clamav"
	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/checks"
	"yundu/internal/config"
	"yundu/internal/integrations"
	"yundu/internal/secrets"
)

type Service struct {
	db           *sql.DB
	integrations *integrations.Service
	secrets      *secrets.Store
	antivirus    config.Antivirus
	worker       string
}
type claimed struct {
	jobID, requestID, fileID, sourceStorage, targetStorage, expectedHash, candidate, name, sourceKey, sourceVersion, direction string
	size                                                                                                                       int64
	token, slotToken                                                                                                           uint64
	slot                                                                                                                       int
	attempt                                                                                                                    int
	antivirusRequired                                                                                                          bool
}
type Job struct {
	ID            string    `json:"id"`
	RequestID     string    `json:"request_id"`
	Filename      string    `json:"filename"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

var (
	ErrQuarantined = errors.New("transfer quarantined")
	ErrStaleLease  = errors.New("stale transfer lease")
)

func New(db *sql.DB, i *integrations.Service, s *secrets.Store, a config.Antivirus) *Service {
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	return &Service{db: db, integrations: i, secrets: s, antivirus: a, worker: "transfer-" + hex.EncodeToString(random)}
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishInternalOutbox(ctx)
			s.runOne(ctx)
		}
	}
}

// The monolith uses the transactional outbox as its durable hand-off boundary.
// M6 has no external broker; the in-process consumer acknowledges only events
// whose durable transfer work (or completed release) is already present.
func (s *Service) publishInternalOutbox(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `UPDATE outbox_events o SET o.status='PUBLISHED',o.published_at=UTC_TIMESTAMP(6) WHERE o.status='PENDING' AND (o.event_type='REQUEST_RELEASED' OR (o.event_type='TRANSFER_REQUESTED' AND EXISTS(SELECT 1 FROM transfer_jobs j WHERE j.request_id=o.aggregate_id))) ORDER BY o.created_at LIMIT 100`)
}
func (s *Service) runOne(ctx context.Context) {
	job, err := s.claim(ctx)
	if err != nil {
		return
	}
	if err = s.execute(ctx, job); err != nil && !errors.Is(err, ErrQuarantined) && !errors.Is(err, ErrStaleLease) {
		s.fail(ctx, job, "TRANSFER_FAILED", err)
	}
}
func (s *Service) claim(ctx context.Context) (claimed, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return claimed{}, err
	}
	defer tx.Rollback()
	var frozen bool
	if err = tx.QueryRowContext(ctx, `SELECT recovery_freeze FROM system_guard WHERE id=1`).Scan(&frozen); err != nil {
		return claimed{}, err
	}
	if frozen {
		return claimed{}, sql.ErrNoRows
	}
	var job claimed
	var jobID, requestID, fileID, source, target, hash []byte
	err = tx.QueryRowContext(ctx, `SELECT j.id,j.request_id,j.file_id,j.source_storage_version_id,j.target_storage_version_id,j.expected_sha256,j.expected_bytes,j.attempts,j.lease_token,f.original_name,f.object_key,f.source_version_id,r.direction,COALESCE(JSON_EXTRACT(r.frozen_snapshot,'$.target_antivirus_required'),FALSE) FROM transfer_jobs j JOIN request_files f ON f.id=j.file_id JOIN exchange_requests r ON r.id=j.request_id WHERE (j.status IN ('PENDING','FAILED') AND j.available_at<=UTC_TIMESTAMP(6)) OR (j.status='RUNNING' AND j.lease_expires_at<UTC_TIMESTAMP(6)) ORDER BY j.created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&jobID, &requestID, &fileID, &source, &target, &hash, &job.size, &job.attempt, &job.token, &job.name, &job.sourceKey, &job.sourceVersion, &job.direction, &job.antivirusRequired)
	if err != nil {
		return claimed{}, err
	}
	var slot int
	var slotToken uint64
	if err = tx.QueryRowContext(ctx, "SELECT slot_no,fencing_token FROM resource_slots WHERE kind='COPY' AND (holder_id IS NULL OR lease_expires_at<UTC_TIMESTAMP(6)) ORDER BY slot_no LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&slot, &slotToken); err != nil {
		return claimed{}, err
	}
	job.token++
	job.slotToken = slotToken + 1
	job.slot = slot
	job.attempt++
	job.candidate = "requests/" + hex.EncodeToString(requestID) + "/candidates/" + randomHex(16)
	lease := time.Now().UTC().Add(90 * time.Second)
	if _, err = tx.ExecContext(ctx, "UPDATE resource_slots SET holder_id=?,fencing_token=?,lease_expires_at=? WHERE kind='COPY' AND slot_no=?", jobID, job.slotToken, lease, slot); err != nil {
		return claimed{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE transfer_jobs SET status='RUNNING',lease_owner=?,lease_token=?,lease_expires_at=?,attempts=?,candidate_key=? WHERE id=?", s.worker, job.token, lease, job.attempt, job.candidate, jobID); err != nil {
		return claimed{}, err
	}
	attemptID := make([]byte, 16)
	_, _ = rand.Read(attemptID)
	if _, err = tx.ExecContext(ctx, "INSERT INTO transfer_attempts(id,transfer_job_id,attempt_no,fencing_token,worker_instance_id,candidate_key,status) VALUES(?,?,?,?,?,?,'RUNNING')", attemptID, jobID, job.attempt, job.token, s.worker, job.candidate); err != nil {
		return claimed{}, err
	}
	if err = tx.Commit(); err != nil {
		return claimed{}, err
	}
	job.jobID = hex.EncodeToString(jobID)
	job.requestID = hex.EncodeToString(requestID)
	job.fileID = hex.EncodeToString(fileID)
	job.sourceStorage = hex.EncodeToString(source)
	job.targetStorage = hex.EncodeToString(target)
	job.expectedHash = hex.EncodeToString(hash)
	return job, nil
}
func (s *Service) execute(ctx context.Context, j claimed) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	leaseDone := make(chan struct{})
	go s.renewLease(workCtx, j, cancel, leaseDone)
	defer func() { cancel(); <-leaseDone }()
	ctx = workCtx
	source, err := s.integrations.GetStorageVersion(ctx, j.sourceStorage)
	if err != nil {
		return err
	}
	target, err := s.integrations.GetStorageVersion(ctx, j.targetStorage)
	if err != nil {
		return err
	}
	sourceAccess, err := s.secrets.Get(ctx, source.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		return err
	}
	sourceSecret, err := s.secrets.Get(ctx, source.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		clear(sourceAccess)
		return err
	}
	targetAccess, err := s.secrets.Get(ctx, target.Config.AccessKeySecretID, "S3_ACCESS_KEY")
	if err != nil {
		clear(sourceAccess)
		clear(sourceSecret)
		return err
	}
	targetSecret, err := s.secrets.Get(ctx, target.Config.SecretKeySecretID, "S3_SECRET_KEY")
	if err != nil {
		clear(sourceAccess)
		clear(sourceSecret)
		clear(targetAccess)
		return err
	}
	defer func() { clear(sourceAccess); clear(sourceSecret); clear(targetAccess); clear(targetSecret) }()
	reader, size, err := s3adapter.OpenVersion(ctx, source.Config, j.sourceKey, j.sourceVersion, string(sourceAccess), string(sourceSecret))
	if err != nil {
		return err
	}
	if size != j.size {
		reader.Close()
		return errors.New("source size mismatch")
	}
	targetKey := strings.Trim(target.Config.Prefix, "/")
	if targetKey != "" {
		targetKey += "/"
	}
	targetKey += j.candidate
	uploaded, err := s3adapter.Upload(ctx, target.Config, targetKey, "application/octet-stream", reader, j.size, string(targetAccess), string(targetSecret))
	reader.Close()
	if err != nil {
		return err
	}
	if uploaded.SHA256 != j.expectedHash {
		return s.quarantine(ctx, j, uploaded.VersionID, uploaded.SHA256, "COPY_HASH_MISMATCH")
	}
	targetReader, targetSize, err := s3adapter.OpenVersion(ctx, target.Config, targetKey, uploaded.VersionID, string(targetAccess), string(targetSecret))
	if err != nil {
		return err
	}
	result, err := checks.InspectFile(j.direction, j.name, targetReader)
	targetReader.Close()
	if err != nil {
		return err
	}
	if targetSize != j.size || result.CheckedBytes != j.size || result.SHA256 != j.expectedHash || result.Status != "PASSED" {
		return s.quarantine(ctx, j, uploaded.VersionID, result.SHA256, "TARGET_VERIFICATION_FAILED")
	}
	var virus *clamav.Result
	if j.antivirusRequired {
		if !s.antivirus.Enabled {
			return errors.New("target antivirus is required but disabled")
		}
		virusReader, _, openErr := s3adapter.OpenVersion(ctx, target.Config, targetKey, uploaded.VersionID, string(targetAccess), string(targetSecret))
		if openErr != nil {
			return openErr
		}
		vr, scanErr := clamav.Scan(ctx, s.antivirus.Address, s.antivirus.Timeout, virusReader)
		virusReader.Close()
		if scanErr != nil {
			return scanErr
		}
		virus = &vr
		if vr.Status != "PASSED" {
			return s.quarantine(ctx, j, uploaded.VersionID, result.SHA256, "TARGET_VIRUS_DETECTED")
		}
	}
	return s.succeed(ctx, j, target.Config.Bucket, targetKey, uploaded.VersionID, result, virus)
}
func (s *Service) succeed(ctx context.Context, j claimed, bucket, targetKey, versionID string, result checks.Result, virus *clamav.Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	jobID, _ := hex.DecodeString(j.jobID)
	fileID, _ := hex.DecodeString(j.fileID)
	requestID, _ := hex.DecodeString(j.requestID)
	digest, _ := hex.DecodeString(result.SHA256)
	res, err := tx.ExecContext(ctx, "UPDATE transfer_jobs SET status='SUCCEEDED',lease_owner=NULL,lease_expires_at=NULL WHERE id=? AND status='RUNNING' AND lease_token=?", jobID, j.token)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("stale fencing token")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE transfer_attempts SET status='SUCCEEDED',bytes_copied=?,target_version_id=?,computed_sha256=?,finished_at=UTC_TIMESTAMP(6) WHERE transfer_job_id=? AND attempt_no=? AND fencing_token=?", j.size, versionID, digest, jobID, j.attempt, j.token); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_files SET target_storage_revision_id=UNHEX(?),target_bucket=?,target_object_key=?,target_version_id=?,target_sha256=?,status='READY',version=version+1 WHERE id=?", j.targetStorage, bucket, targetKey, versionID, digest, fileID); err != nil {
		return err
	}
	if err = s.saveTargetChecks(ctx, tx, requestID, fileID, result, virus); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='COPY' AND slot_no=? AND fencing_token=?", j.slot, j.slotToken); err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM transfer_jobs WHERE request_id=? AND status<>'SUCCEEDED'", requestID).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='READY',published_at=UTC_TIMESTAMP(6),expires_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL IF(direction='OFFICE_TO_PROD',8,24) HOUR),version=version+1 WHERE id=? AND status IN ('TRANSFER_PENDING','COPYING','APPROVED')", requestID); err != nil {
			return err
		}
		outboxID := make([]byte, 16)
		_, _ = rand.Read(outboxID)
		payload, _ := json.Marshal(map[string]any{"request_id": j.requestID})
		if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO outbox_events(id,event_type,aggregate_type,aggregate_id,aggregate_version,dedupe_key,payload) VALUES(?,'REQUEST_RELEASED','REQUEST',?,1,?,?)", outboxID, requestID, "request-released:"+j.requestID, payload); err != nil {
			return err
		}
	} else {
		_, _ = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='COPYING' WHERE id=? AND status='TRANSFER_PENDING'", requestID)
	}
	return tx.Commit()
}
func (s *Service) saveTargetChecks(ctx context.Context, tx *sql.Tx, requestID, fileID []byte, result checks.Result, virus *clamav.Result) error {
	raw, _ := json.Marshal(result)
	digest, _ := hex.DecodeString(result.SHA256)
	for _, kind := range []string{"TYPE", "HASH"} {
		checkID := make([]byte, 16)
		_, _ = rand.Read(checkID)
		summary := raw
		if kind == "HASH" {
			summary = []byte(`{"matches_source_hash":true}`)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO file_check_results(id,request_id,file_id,stage,check_type,required,status,rule_version,engine,sha256,checked_bytes,coverage_complete,report_summary,started_at,finished_at) VALUES(?,?,?,'TARGET',?,TRUE,'PASSED','m6-target-v1','yundu-transfer',?,?,TRUE,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, checkID, requestID, fileID, kind, digest, result.CheckedBytes, summary); err != nil {
			return err
		}
	}
	virusID := make([]byte, 16)
	_, _ = rand.Read(virusID)
	if virus != nil {
		report, _ := json.Marshal(virus)
		_, err := tx.ExecContext(ctx, `INSERT INTO file_check_results(id,request_id,file_id,stage,check_type,required,status,rule_version,engine,checked_bytes,coverage_complete,report_summary,started_at,finished_at) VALUES(?,?,?,'TARGET','VIRUS',TRUE,'PASSED','m6-target-v1','clamav',?,TRUE,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, virusID, requestID, fileID, result.CheckedBytes, report)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO file_check_results(id,request_id,file_id,stage,check_type,required,status,rule_version,engine,checked_bytes,coverage_complete,report_summary,started_at,finished_at) VALUES(?,?,?,'TARGET','VIRUS',FALSE,'SKIPPED_DISABLED','m6-target-v1','yundu-transfer',0,FALSE,JSON_OBJECT('message','杀毒未启用；未执行病毒扫描'),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, virusID, requestID, fileID)
	return err
}
func (s *Service) quarantine(ctx context.Context, j claimed, versionID, hash, code string) error {
	jobID, _ := hex.DecodeString(j.jobID)
	fileID, _ := hex.DecodeString(j.fileID)
	requestID, _ := hex.DecodeString(j.requestID)
	digest, _ := hex.DecodeString(hash)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE transfer_jobs SET status='QUARANTINED',last_error_code=?,lease_owner=NULL,lease_expires_at=NULL WHERE id=? AND status='RUNNING' AND lease_token=?", code, jobID, j.token)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return ErrStaleLease
	}
	_, _ = tx.ExecContext(ctx, "UPDATE transfer_attempts SET status='QUARANTINED',target_version_id=?,computed_sha256=?,error_code=?,finished_at=UTC_TIMESTAMP(6) WHERE transfer_job_id=? AND attempt_no=?", versionID, digest, code, jobID, j.attempt)
	_, _ = tx.ExecContext(ctx, "UPDATE request_files SET status='QUARANTINED' WHERE id=?", fileID)
	_, _ = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='QUARANTINED',version=version+1 WHERE id=?", requestID)
	_, _ = tx.ExecContext(ctx, "UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='COPY' AND slot_no=? AND fencing_token=?", j.slot, j.slotToken)
	if err = tx.Commit(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrQuarantined, code)
}
func (s *Service) fail(ctx context.Context, j claimed, code string, cause error) {
	jobID, _ := hex.DecodeString(j.jobID)
	status := "FAILED"
	if j.attempt >= 5 {
		status = "DEAD"
	}
	delay := RetryDelay(j.attempt)
	detail := safe(cause.Error())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	res, _ := tx.ExecContext(ctx, "UPDATE transfer_jobs SET status=?,available_at=?,lease_owner=NULL,lease_expires_at=NULL,last_error_code=?,last_error_detail=? WHERE id=? AND status='RUNNING' AND lease_token=?", status, time.Now().UTC().Add(delay), code, detail, jobID, j.token)
	changed, _ := res.RowsAffected()
	if changed != 1 {
		return
	}
	_, _ = tx.ExecContext(ctx, "UPDATE transfer_attempts SET status='FAILED',error_code=?,finished_at=UTC_TIMESTAMP(6) WHERE transfer_job_id=? AND attempt_no=?", code, jobID, j.attempt)
	_, _ = tx.ExecContext(ctx, "UPDATE resource_slots SET holder_id=NULL,lease_expires_at=NULL WHERE kind='COPY' AND slot_no=? AND fencing_token=?", j.slot, j.slotToken)
	if status == "DEAD" {
		requestID, _ := hex.DecodeString(j.requestID)
		_, _ = tx.ExecContext(ctx, "UPDATE exchange_requests SET status='BLOCKED',version=version+1 WHERE id=?", requestID)
	}
	_ = tx.Commit()
}
func (s *Service) List(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(j.id),HEX(j.request_id),f.original_name,j.status,j.attempts,COALESCE(j.last_error_code,''),j.updated_at FROM transfer_jobs j JOIN request_files f ON f.id=j.file_id ORDER BY j.updated_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.RequestID, &j.Filename, &j.Status, &j.Attempts, &j.LastErrorCode, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.ID = strings.ToLower(j.ID)
		j.RequestID = strings.ToLower(j.RequestID)
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Service) Retry(ctx context.Context, jobHex string) error {
	id, err := hex.DecodeString(jobHex)
	if err != nil || len(id) != 16 {
		return errors.New("invalid id")
	}
	res, err := s.db.ExecContext(ctx, "UPDATE transfer_jobs SET status='PENDING',available_at=UTC_TIMESTAMP(6),last_error_code=NULL,last_error_detail=NULL WHERE id=? AND status IN ('FAILED','DEAD')", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("job is not retryable")
	}
	return nil
}

func (s *Service) renewLease(ctx context.Context, j claimed, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	jobID, _ := hex.DecodeString(j.jobID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lease := time.Now().UTC().Add(90 * time.Second)
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				cancel()
				return
			}
			jobResult, err := tx.ExecContext(ctx, "UPDATE transfer_jobs SET lease_expires_at=? WHERE id=? AND status='RUNNING' AND lease_owner=? AND lease_token=?", lease, jobID, s.worker, j.token)
			if err == nil {
				slotResult, slotErr := tx.ExecContext(ctx, "UPDATE resource_slots SET lease_expires_at=? WHERE kind='COPY' AND slot_no=? AND holder_id=? AND fencing_token=?", lease, j.slot, jobID, j.slotToken)
				jobRows, _ := jobResult.RowsAffected()
				slotRows, _ := slotResult.RowsAffected()
				if slotErr != nil || jobRows != 1 || slotRows != 1 {
					err = ErrStaleLease
				}
			}
			if err != nil || tx.Commit() != nil {
				_ = tx.Rollback()
				cancel()
				return
			}
		}
	}
}

func randomHex(bytes int) string {
	b := make([]byte, bytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(1<<min(attempt-1, 4)) * time.Minute
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func safe(v string) string {
	v = strings.ReplaceAll(v, "\n", " ")
	if len(v) > 500 {
		v = v[:500]
	}
	return v
}
