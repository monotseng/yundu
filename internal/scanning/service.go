package scanning

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"yundu/internal/adapters/clamav"
	s3adapter "yundu/internal/adapters/s3"
	"yundu/internal/checks"
	"yundu/internal/config"
	"yundu/internal/integrations"
	"yundu/internal/secrets"
	"yundu/internal/workflow"
)

type Service struct {
	db           *sql.DB
	integrations *integrations.Service
	secrets      *secrets.Store
	antivirus    config.Antivirus
	workflow     *workflow.Service
}
type fileRow struct {
	id, name, key, storageVersion, bucket, versionID, expectedHash string
	size                                                           int64
}
type Check struct {
	FileID           string `json:"file_id"`
	Filename         string `json:"filename"`
	CheckType        string `json:"check_type"`
	Required         bool   `json:"required"`
	Status           string `json:"status"`
	CheckedBytes     string `json:"checked_bytes"`
	CoverageComplete bool   `json:"coverage_complete"`
	Summary          any    `json:"summary"`
}

func New(db *sql.DB, i *integrations.Service, s *secrets.Store, a config.Antivirus, w *workflow.Service) *Service {
	return &Service{db: db, integrations: i, secrets: s, antivirus: a, workflow: w}
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOne(ctx)
		}
	}
}
func (s *Service) runOne(ctx context.Context) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	var id []byte
	var raw []byte
	var attempts int
	err = tx.QueryRowContext(ctx, `SELECT id,payload,attempts FROM jobs WHERE type='SCAN_SOURCE' AND status='PENDING' AND available_at<=UTC_TIMESTAMP(6) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &raw, &attempts)
	if err != nil {
		tx.Rollback()
		return
	}
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET status='RUNNING',lease_holder='m4-scanner',lease_token=lease_token+1,lease_expires_at=UTC_TIMESTAMP(6)+INTERVAL 90 SECOND,attempts=attempts+1 WHERE id=?", id); err != nil {
		tx.Rollback()
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	var payload struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	err = s.ScanRequest(ctx, payload.RequestID)
	status := "SUCCEEDED"
	var code, detail any
	if err != nil {
		status = "PENDING"
		code = "SCAN_FAILED"
		detail = safe(err.Error())
		if attempts+1 >= 5 {
			status = "FAILED"
			if requestID, decodeErr := decode(payload.RequestID); decodeErr == nil {
				_, _ = s.db.ExecContext(ctx, "UPDATE exchange_requests SET status='SCAN_FAILED',version=version+1 WHERE id=? AND status='SCANNING'", requestID)
			}
		}
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE jobs SET status=?,available_at=IF(?='PENDING',UTC_TIMESTAMP(6)+INTERVAL 1 MINUTE,available_at),lease_holder=NULL,lease_expires_at=NULL,last_error_code=?,last_error_detail=? WHERE id=?", status, status, code, detail, id)
}
func (s *Service) ScanRequest(ctx context.Context, requestHex string) error {
	requestID, err := decode(requestHex)
	if err != nil {
		return err
	}
	var direction, status string
	var snapshot []byte
	if err = s.db.QueryRowContext(ctx, "SELECT direction,status,frozen_snapshot FROM exchange_requests WHERE id=?", requestID).Scan(&direction, &status, &snapshot); err != nil {
		return err
	}
	if status != "SCANNING" {
		return nil
	}
	var frozen struct {
		AntivirusRequired bool `json:"antivirus_required"`
	}
	if err = json.Unmarshal(snapshot, &frozen); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(f.id),f.original_name,f.object_key,HEX(f.source_storage_revision_id),f.source_bucket,f.source_version_id,LOWER(HEX(f.source_sha256)),f.size_bytes FROM request_files f WHERE f.request_id=? AND f.status='UPLOADED' ORDER BY f.created_at`, requestID)
	if err != nil {
		return err
	}
	files := []fileRow{}
	for rows.Next() {
		var f fileRow
		if err = rows.Scan(&f.id, &f.name, &f.key, &f.storageVersion, &f.bucket, &f.versionID, &f.expectedHash, &f.size); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	rows.Close()
	allPassed := true
	technical := false
	for _, f := range files {
		version, err := s.integrations.GetStorageVersion(ctx, f.storageVersion)
		if err != nil {
			technical = true
			allPassed = false
			continue
		}
		access, err := s.secrets.Get(ctx, version.Config.AccessKeySecretID, "S3_ACCESS_KEY")
		if err != nil {
			technical = true
			allPassed = false
			continue
		}
		secret, err := s.secrets.Get(ctx, version.Config.SecretKeySecretID, "S3_SECRET_KEY")
		if err != nil {
			clear(access)
			technical = true
			allPassed = false
			continue
		}
		reader, actualSize, err := s3adapter.OpenVersion(ctx, version.Config, f.key, f.versionID, string(access), string(secret))
		if err != nil {
			clear(access)
			clear(secret)
			technical = true
			allPassed = false
			continue
		}
		result, inspectErr := checks.InspectFile(direction, f.name, reader)
		reader.Close()
		if inspectErr != nil {
			result.Status = "ERROR"
			result.CoverageComplete = false
		}
		if actualSize != f.size || result.CheckedBytes != f.size {
			result.Status = "FAILED"
			result.CoverageComplete = false
			result.RiskCodes = append(result.RiskCodes, "SIZE_MISMATCH")
		}
		checkType := "TYPE"
		if direction == "OFFICE_TO_PROD" {
			checkType = "TEXT"
		}
		if err = s.saveCheck(ctx, requestID, f.id, checkType, true, result.Status, result.SHA256, result.CheckedBytes, result.CoverageComplete, result); err != nil {
			return err
		}
		hashStatus := "PASSED"
		if !strings.EqualFold(result.SHA256, f.expectedHash) {
			hashStatus = "FAILED"
		}
		if err = s.saveCheck(ctx, requestID, f.id, "HASH", true, hashStatus, result.SHA256, result.CheckedBytes, result.CoverageComplete, map[string]any{"matches_uploaded_hash": hashStatus == "PASSED"}); err != nil {
			return err
		}
		if result.Status != "PASSED" || hashStatus != "PASSED" {
			allPassed = false
		}
		if !frozen.AntivirusRequired {
			if err = s.saveCheck(ctx, requestID, f.id, "VIRUS", false, "SKIPPED_DISABLED", "", 0, false, map[string]any{"message": "杀毒未启用；未执行病毒扫描"}); err != nil {
				return err
			}
		} else if !s.antivirus.Enabled {
			technical = true
			allPassed = false
			if err = s.saveCheck(ctx, requestID, f.id, "VIRUS", true, "ERROR", "", 0, false, map[string]any{"message": "冻结策略要求杀毒，但扫描器未启用"}); err != nil {
				return err
			}
		} else {
			virusReader, _, openErr := s3adapter.OpenVersion(ctx, version.Config, f.key, f.versionID, string(access), string(secret))
			if openErr != nil {
				technical = true
				allPassed = false
				if err = s.saveCheck(ctx, requestID, f.id, "VIRUS", true, "ERROR", "", 0, false, map[string]any{"message": "无法读取精确对象版本"}); err != nil {
					return err
				}
			} else {
				virusCtx, cancel := context.WithTimeout(ctx, s.antivirus.Timeout)
				virusResult, scanErr := clamav.Scan(virusCtx, s.antivirus.Address, s.antivirus.Timeout, virusReader)
				cancel()
				virusReader.Close()
				if scanErr != nil {
					technical = true
					allPassed = false
				}
				if virusResult.Status != "PASSED" {
					allPassed = false
				}
				if err = s.saveCheck(ctx, requestID, f.id, "VIRUS", true, virusResult.Status, "", 0, virusResult.Status == "PASSED", virusResult); err != nil {
					return err
				}
			}
		}
		clear(access)
		clear(secret)
	}
	next := "IN_REVIEW"
	if !allPassed {
		next = "REJECTED_CHECK"
	}
	if technical {
		next = "SCAN_FAILED"
	}
	_, err = s.db.ExecContext(ctx, "UPDATE exchange_requests SET status=?,version=version+1 WHERE id=? AND status='SCANNING'", next, requestID)
	if err != nil {
		return err
	}
	if next == "IN_REVIEW" && s.workflow != nil {
		if startErr := s.workflow.StartRequest(ctx, requestHex); startErr != nil {
			_, _ = s.db.ExecContext(ctx, "UPDATE exchange_requests SET status='WORKFLOW_BLOCKED',version=version+1 WHERE id=? AND status='IN_REVIEW'", requestID)
		}
	}
	return nil
}
func (s *Service) saveCheck(ctx context.Context, requestID []byte, fileHex, kind string, required bool, status, sha string, checked int64, coverage bool, summary any) error {
	fileID, err := decode(fileHex)
	if err != nil {
		return err
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	raw, _ := json.Marshal(summary)
	var digest any
	if sha != "" {
		digest, _ = hex.DecodeString(sha)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO file_check_results(id,request_id,file_id,stage,check_type,required,status,rule_version,engine,sha256,checked_bytes,coverage_complete,report_summary,started_at,finished_at) VALUES(?,?,?,'SOURCE',?,?,?,'m4-source-v1','yundu-checker',?,?,?, ?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE required=VALUES(required),status=VALUES(status),sha256=VALUES(sha256),checked_bytes=VALUES(checked_bytes),coverage_complete=VALUES(coverage_complete),report_summary=VALUES(report_summary),finished_at=VALUES(finished_at)`, id, requestID, fileID, kind, required, status, digest, checked, coverage, raw)
	return err
}
func (s *Service) ListChecks(ctx context.Context, requestHex, userHex string) ([]Check, error) {
	requestID, err := decode(requestHex)
	if err != nil {
		return nil, err
	}
	userID, err := decode(userHex)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(c.file_id),f.original_name,c.check_type,c.required,c.status,CAST(c.checked_bytes AS CHAR),c.coverage_complete,c.report_summary FROM file_check_results c JOIN request_files f ON f.id=c.file_id JOIN exchange_requests r ON r.id=c.request_id WHERE c.request_id=? AND r.requester_id=? ORDER BY f.created_at,c.check_type`, requestID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Check{}
	for rows.Next() {
		var c Check
		var raw []byte
		if err = rows.Scan(&c.FileID, &c.Filename, &c.CheckType, &c.Required, &c.Status, &c.CheckedBytes, &c.CoverageComplete, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &c.Summary)
		c.FileID = strings.ToLower(c.FileID)
		out = append(out, c)
	}
	return out, rows.Err()
}
func decode(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
func safe(v string) string {
	v = strings.ReplaceAll(v, "\n", " ")
	if len(v) > 500 {
		v = v[:500]
	}
	return v
}

var _ = bytes.Equal
