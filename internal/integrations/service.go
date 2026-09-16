package integrations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yundu/internal/secrets"
)

type Service struct {
	db      *sql.DB
	secrets *secrets.Store
}
type Definition struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	Zone             string  `json:"zone"`
	Status           string  `json:"status"`
	Version          uint64  `json:"version"`
	CurrentRevision  *uint64 `json:"current_revision,omitempty"`
	CurrentVersionID string  `json:"current_version_id,omitempty"`
	ProbePassed      bool    `json:"probe_passed"`
}
type S3Config struct {
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	Bucket             string `json:"bucket"`
	Prefix             string `json:"prefix"`
	UseTLS             bool   `json:"use_tls"`
	PathStyle          bool   `json:"path_style"`
	VersioningRequired bool   `json:"versioning_required"`
	SSEMode            string `json:"sse_mode,omitempty"`
	AccessKeySecretID  string `json:"access_key_secret_id"`
	SecretKeySecretID  string `json:"secret_key_secret_id"`
}
type Version struct {
	ID     string   `json:"id"`
	Config S3Config `json:"config"`
}
type S3DraftDetail struct {
	Definition Definition `json:"definition"`
	Version    Version    `json:"version"`
}
type Channel struct {
	ID                     string `json:"id"`
	Direction              string `json:"direction"`
	SourceStorageVersionID string `json:"source_storage_version_id"`
	TargetStorageVersionID string `json:"target_storage_version_id"`
	AntivirusEnabled       bool   `json:"antivirus_enabled"`
	Status                 string `json:"status"`
	Version                uint64 `json:"version"`
}

func (s *Service) GetPublishedStorage(ctx context.Context, zone string) (Version, error) {
	zone = strings.ToUpper(zone)
	var versionID []byte
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT v.id,v.config_json FROM integration_definitions d JOIN integration_versions v ON v.id=d.current_version_id WHERE d.type='S3_STORAGE' AND d.zone=? AND d.status='PUBLISHED' AND v.status='PUBLISHED' ORDER BY d.updated_at DESC LIMIT 1`, zone).Scan(&versionID, &raw)
	if err != nil {
		return Version{}, err
	}
	var cfg S3Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return Version{}, err
	}
	return Version{ID: hex.EncodeToString(versionID), Config: cfg}, nil
}
func (s *Service) GetStorageVersion(ctx context.Context, versionHex string) (Version, error) {
	idBytes, err := decode(versionHex)
	if err != nil {
		return Version{}, err
	}
	var raw []byte
	var idValue []byte
	if err = s.db.QueryRowContext(ctx, `SELECT id,config_json FROM integration_versions WHERE id=? AND status='PUBLISHED'`, idBytes).Scan(&idValue, &raw); err != nil {
		return Version{}, err
	}
	var cfg S3Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return Version{}, err
	}
	return Version{ID: hex.EncodeToString(idValue), Config: cfg}, nil
}

func (s *Service) GetUsableStorageVersion(ctx context.Context, versionHex string) (Version, error) {
	idBytes, err := decode(versionHex)
	if err != nil {
		return Version{}, err
	}
	var raw, idValue []byte
	err = s.db.QueryRowContext(ctx, `SELECT v.id,v.config_json FROM integration_versions v JOIN integration_definitions d ON d.id=v.integration_id WHERE v.id=? AND v.status='PUBLISHED' AND d.status='PUBLISHED'`, idBytes).Scan(&idValue, &raw)
	if err != nil {
		return Version{}, err
	}
	var cfg S3Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return Version{}, err
	}
	return Version{ID: hex.EncodeToString(idValue), Config: cfg}, nil
}

func (s *Service) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(id),direction,HEX(source_storage_version_id),HEX(target_storage_version_id),antivirus_enabled,status,version FROM exchange_channels ORDER BY direction`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Channel{}
	for rows.Next() {
		var c Channel
		if err = rows.Scan(&c.ID, &c.Direction, &c.SourceStorageVersionID, &c.TargetStorageVersionID, &c.AntivirusEnabled, &c.Status, &c.Version); err != nil {
			return nil, err
		}
		c.ID, c.SourceStorageVersionID, c.TargetStorageVersionID = strings.ToLower(c.ID), strings.ToLower(c.SourceStorageVersionID), strings.ToLower(c.TargetStorageVersionID)
		items = append(items, c)
	}
	return items, rows.Err()
}

func (s *Service) PublishChannel(ctx context.Context, direction, sourceHex, targetHex string, antivirus bool, expectedVersion uint64, actorHex string) error {
	direction = strings.ToUpper(direction)
	if direction != "PROD_TO_OFFICE" && direction != "OFFICE_TO_PROD" {
		return errors.New("invalid direction")
	}
	source, err := decode(sourceHex)
	if err != nil {
		return errors.New("invalid source storage version")
	}
	target, err := decode(targetHex)
	if err != nil {
		return errors.New("invalid target storage version")
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	expectedSource, expectedTarget := "PRODUCTION", "OFFICE"
	if direction == "OFFICE_TO_PROD" {
		expectedSource, expectedTarget = "OFFICE", "PRODUCTION"
	}
	var valid int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_versions v JOIN integration_definitions d ON d.id=v.integration_id WHERE v.id=? AND v.status='PUBLISHED' AND d.status='PUBLISHED' AND d.type='S3_STORAGE' AND d.zone=?`, source, expectedSource).Scan(&valid); err != nil || valid != 1 {
		return errors.New("source storage is not a published version in the required zone")
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_versions v JOIN integration_definitions d ON d.id=v.integration_id WHERE v.id=? AND v.status='PUBLISHED' AND d.status='PUBLISHED' AND d.type='S3_STORAGE' AND d.zone=?`, target, expectedTarget).Scan(&valid); err != nil || valid != 1 {
		return errors.New("target storage is not a published version in the required zone")
	}
	idBytes, _ := id()
	result, err := s.db.ExecContext(ctx, `INSERT INTO exchange_channels(id,direction,source_storage_version_id,target_storage_version_id,antivirus_enabled,status,version,updated_by) VALUES(?,?,?,?,?,'PUBLISHED',1,?) ON DUPLICATE KEY UPDATE source_storage_version_id=IF(version=?,VALUES(source_storage_version_id),source_storage_version_id),target_storage_version_id=IF(version=?,VALUES(target_storage_version_id),target_storage_version_id),antivirus_enabled=IF(version=?,VALUES(antivirus_enabled),antivirus_enabled),status=IF(version=?,'PUBLISHED',status),updated_by=IF(version=?,VALUES(updated_by),updated_by),version=IF(version=?,version+1,version)`, idBytes, direction, source, target, antivirus, actor, expectedVersion, expectedVersion, expectedVersion, expectedVersion, expectedVersion, expectedVersion)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return errors.New("channel version conflict")
	}
	return nil
}

func New(db *sql.DB, secretStore *secrets.Store) *Service {
	return &Service{db: db, secrets: secretStore}
}
func (s *Service) CreateS3Draft(ctx context.Context, name, zone string, cfg S3Config, actorHex string) (Definition, error) {
	name = strings.TrimSpace(name)
	zone = strings.ToUpper(zone)
	if name == "" || (zone != "OFFICE" && zone != "PRODUCTION") {
		return Definition{}, errors.New("invalid integration identity")
	}
	if err := ValidateS3Config(cfg); err != nil {
		return Definition{}, err
	}
	var duplicates int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_definitions d JOIN integration_versions v ON v.integration_id=d.id WHERE d.type='S3_STORAGE' AND d.status<>'DISABLED' AND d.zone=? AND JSON_UNQUOTE(JSON_EXTRACT(v.config_json,'$.endpoint'))=? AND JSON_UNQUOTE(JSON_EXTRACT(v.config_json,'$.bucket'))=?`, zone, cfg.Endpoint, cfg.Bucket).Scan(&duplicates); err != nil {
		return Definition{}, err
	}
	if duplicates > 0 {
		return Definition{}, errors.New("storage endpoint and bucket already configured in this zone")
	}
	actor, err := decode(actorHex)
	if err != nil {
		return Definition{}, err
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return Definition{}, err
	}
	digest := sha256.Sum256(configJSON)
	definitionID, _ := id()
	versionID, _ := id()
	accessSecretID, err := decode(cfg.AccessKeySecretID)
	if err != nil {
		return Definition{}, errors.New("invalid access key secret reference")
	}
	secretSecretID, err := decode(cfg.SecretKeySecretID)
	if err != nil {
		return Definition{}, errors.New("invalid secret key secret reference")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO integration_definitions(id,name,type,zone,status,created_by) VALUES(?,?,'S3_STORAGE',?,'DRAFT',?)", definitionID, name, zone, actor); err != nil {
		return Definition{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO integration_versions(id,integration_id,revision,config_json,config_sha256,status,created_by) VALUES(?,?,1,?,?,'DRAFT',?)", versionID, definitionID, configJSON, digest[:], actor); err != nil {
		return Definition{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO integration_version_secrets(integration_version_id,field_name,secret_id) VALUES(?,'access_key',?),(?,'secret_key',?)", versionID, accessSecretID, versionID, secretSecretID); err != nil {
		return Definition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Definition{}, err
	}
	return Definition{ID: hex.EncodeToString(definitionID), Name: name, Type: "S3_STORAGE", Zone: zone, Status: "DRAFT", Version: 1}, nil
}
func ValidateS3Config(c S3Config) error {
	if strings.TrimSpace(c.Endpoint) == "" || strings.Contains(c.Endpoint, "/") || c.Bucket == "" || c.AccessKeySecretID == "" || c.SecretKeySecretID == "" {
		return errors.New("endpoint, bucket, and credential secret references are required")
	}
	if strings.Contains(c.Prefix, "..") || strings.HasPrefix(c.Prefix, "/") {
		return errors.New("unsafe object prefix")
	}
	if !c.VersioningRequired {
		return errors.New("object versioning must be required")
	}
	return nil
}
func (s *Service) Publish(ctx context.Context, definitionHex string, expectedVersion uint64, actorHex string) error {
	definitionID, err := decode(definitionHex)
	if err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version uint64
	var status string
	var draftID []byte
	err = tx.QueryRowContext(ctx, "SELECT version,status FROM integration_definitions WHERE id=? FOR UPDATE", definitionID).Scan(&version, &status)
	if err != nil {
		return err
	}
	if version != expectedVersion || status != "DRAFT" {
		return errors.New("integration version conflict")
	}
	if err = tx.QueryRowContext(ctx, "SELECT id FROM integration_versions WHERE integration_id=? AND status='DRAFT' ORDER BY revision DESC LIMIT 1", definitionID).Scan(&draftID); err != nil {
		return err
	}
	var passed int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM integration_test_runs WHERE integration_version_id=? AND status='PASSED'", draftID).Scan(&passed); err != nil {
		return err
	}
	if passed == 0 {
		return errors.New("integration must pass a persisted probe before publishing")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE integration_versions SET status='PUBLISHED',published_by=?,published_at=? WHERE id=?", actor, now, draftID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE integration_definitions SET status='PUBLISHED',current_version_id=?,version=version+1 WHERE id=?", draftID, definitionID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) List(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(d.id),d.name,d.type,d.zone,d.status,d.version,v.revision,COALESCE(HEX(d.current_version_id),''),EXISTS(SELECT 1 FROM integration_versions dv JOIN integration_test_runs tr ON tr.integration_version_id=dv.id AND tr.status='PASSED' WHERE dv.integration_id=d.id AND dv.status='DRAFT') FROM integration_definitions d LEFT JOIN integration_versions v ON v.id=d.current_version_id ORDER BY d.created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		var revision sql.NullInt64
		if err = rows.Scan(&d.ID, &d.Name, &d.Type, &d.Zone, &d.Status, &d.Version, &revision, &d.CurrentVersionID, &d.ProbePassed); err != nil {
			return nil, err
		}
		d.ID = strings.ToLower(d.ID)
		d.CurrentVersionID = strings.ToLower(d.CurrentVersionID)
		if revision.Valid {
			r := uint64(revision.Int64)
			d.CurrentRevision = &r
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) GetLatestVersion(ctx context.Context, definitionHex string) (Version, error) {
	idBytes, err := decode(definitionHex)
	if err != nil {
		return Version{}, err
	}
	var versionID []byte
	var raw []byte
	err = s.db.QueryRowContext(ctx, "SELECT v.id,v.config_json FROM integration_versions v JOIN integration_definitions d ON d.id=v.integration_id WHERE d.id=? AND d.type='S3_STORAGE' ORDER BY v.revision DESC LIMIT 1", idBytes).Scan(&versionID, &raw)
	if err != nil {
		return Version{}, err
	}
	var cfg S3Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return Version{}, err
	}
	return Version{ID: hex.EncodeToString(versionID), Config: cfg}, nil
}
func (s *Service) GetS3Draft(ctx context.Context, definitionHex string) (S3DraftDetail, error) {
	idBytes, err := decode(definitionHex)
	if err != nil {
		return S3DraftDetail{}, err
	}
	var d Definition
	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT LOWER(HEX(d.id)),d.name,d.type,d.zone,d.status,d.version,LOWER(HEX(v.id)),v.config_json FROM integration_definitions d JOIN integration_versions v ON v.integration_id=d.id WHERE d.id=? AND d.type='S3_STORAGE' AND d.status='DRAFT' AND v.status='DRAFT' ORDER BY v.revision DESC LIMIT 1`, idBytes).Scan(&d.ID, &d.Name, &d.Type, &d.Zone, &d.Status, &d.Version, &d.CurrentVersionID, &raw)
	if err != nil {
		return S3DraftDetail{}, err
	}
	var cfg S3Config
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return S3DraftDetail{}, err
	}
	return S3DraftDetail{Definition: d, Version: Version{ID: d.CurrentVersionID, Config: cfg}}, nil
}
func (s *Service) UpdateS3Draft(ctx context.Context, definitionHex, name, zone string, cfg S3Config, expected uint64) error {
	idBytes, err := decode(definitionHex)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	zone = strings.ToUpper(zone)
	if name == "" || (zone != "OFFICE" && zone != "PRODUCTION") {
		return errors.New("invalid integration identity")
	}
	if err = ValidateS3Config(cfg); err != nil {
		return err
	}
	raw, _ := json.Marshal(cfg)
	digest := sha256.Sum256(raw)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var versionID []byte
	if err = tx.QueryRowContext(ctx, `SELECT v.id FROM integration_definitions d JOIN integration_versions v ON v.integration_id=d.id WHERE d.id=? AND d.type='S3_STORAGE' AND d.status='DRAFT' AND d.version=? AND v.status='DRAFT' ORDER BY v.revision DESC LIMIT 1 FOR UPDATE`, idBytes, expected).Scan(&versionID); err != nil {
		return errors.New("integration draft version conflict")
	}
	accessID, err := decode(cfg.AccessKeySecretID)
	if err != nil {
		return errors.New("invalid access key secret reference")
	}
	secretID, err := decode(cfg.SecretKeySecretID)
	if err != nil {
		return errors.New("invalid secret key secret reference")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE integration_definitions SET name=?,zone=?,version=version+1 WHERE id=?`, name, zone, idBytes); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE integration_versions SET config_json=?,config_sha256=? WHERE id=?`, raw, digest[:], versionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE integration_version_secrets SET secret_id=CASE field_name WHEN 'access_key' THEN ? ELSE ? END WHERE integration_version_id=?`, accessID, secretID, versionID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) DeleteS3Draft(ctx context.Context, definitionHex string) error {
	return s.DeleteDraft(ctx, definitionHex)
}
func (s *Service) DeleteDraft(ctx context.Context, definitionHex string) error {
	idBytes, err := decode(definitionHex)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, integrationType string
	if err = tx.QueryRowContext(ctx, `SELECT status,type FROM integration_definitions WHERE id=? AND type IN ('S3_STORAGE','LLM') FOR UPDATE`, idBytes).Scan(&status, &integrationType); err != nil {
		return err
	}
	_ = integrationType
	if status != "DRAFT" {
		return errors.New("only draft integrations can be deleted")
	}
	if _, err = tx.ExecContext(ctx, `DELETE t FROM integration_test_runs t JOIN integration_versions v ON v.id=t.integration_version_id WHERE v.integration_id=?`, idBytes); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE s FROM integration_version_secrets s JOIN integration_versions v ON v.id=s.integration_version_id WHERE v.integration_id=?`, idBytes); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM integration_versions WHERE integration_id=?`, idBytes); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM integration_definitions WHERE id=?`, idBytes); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) RecordTest(ctx context.Context, versionHex, actorHex, status, stage, errorCode, detail string) error {
	versionID, err := decode(versionHex)
	if err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	testID, _ := id()
	_, err = s.db.ExecContext(ctx, "INSERT INTO integration_test_runs(id,integration_version_id,status,stage,error_code,safe_detail,started_by,finished_at) VALUES(?,?,?,?,?,?,?,UTC_TIMESTAMP(6))", testID, versionID, status, stage, nullable(errorCode), nullable(detail), actor)
	return err
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func id() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func decode(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
