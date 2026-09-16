package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

type Service struct {
	db  *sql.DB
	now func() time.Time
}
type Definition struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Status           string `json:"status"`
	Version          uint64 `json:"version"`
	DraftVersionID   string `json:"draft_version_id,omitempty"`
	DraftStatus      string `json:"draft_status,omitempty"`
	CurrentVersionID string `json:"current_version_id,omitempty"`
}
type Selection struct {
	VersionID string          `json:"version_id"`
	Graph     json.RawMessage `json:"graph"`
	Hash      string          `json:"graph_hash"`
}
type VersionDetail struct {
	ID            string          `json:"id"`
	DefinitionID  string          `json:"definition_id"`
	VersionNumber uint64          `json:"version_number"`
	Version       uint64          `json:"version"`
	Status        string          `json:"status"`
	Graph         json.RawMessage `json:"graph"`
	Layout        json.RawMessage `json:"layout"`
}
type Binding struct {
	ID                string `json:"id"`
	Direction         string `json:"direction"`
	ScopeType         string `json:"scope_type"`
	ScopeID           string `json:"scope_id,omitempty"`
	WorkflowVersionID string `json:"workflow_version_id"`
	WorkflowCode      string `json:"workflow_code"`
	WorkflowName      string `json:"workflow_name"`
	VersionNumber     uint64 `json:"version_number"`
	Enabled           bool   `json:"enabled"`
}
type Task struct {
	ID        string     `json:"id"`
	RequestID string     `json:"request_id"`
	RequestNo string     `json:"request_no"`
	NodeID    string     `json:"node_id"`
	NodeName  string     `json:"node_name"`
	Mode      string     `json:"mode"`
	Status    string     `json:"status"`
	DueAt     *time.Time `json:"due_at,omitempty"`
	Version   uint64     `json:"version"`
}

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{1,63}$`)

func New(db *sql.DB) *Service { return &Service{db: db, now: time.Now} }
func (s *Service) Create(ctx context.Context, code, name, description string, g Graph, actorHex string) (Definition, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	name = strings.TrimSpace(name)
	if !codePattern.MatchString(code) || name == "" {
		return Definition{}, errors.New("invalid workflow")
	}
	actor, err := decode(actorHex)
	if err != nil {
		return Definition{}, err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return Definition{}, err
	}
	digest := sha256.Sum256(raw)
	definitionID, _ := id()
	versionID, _ := id()
	scope := []byte(`{"directions":["PROD_TO_OFFICE","OFFICE_TO_PROD"]}`)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_definitions(id,code,name,description,status,scope_json,created_by) VALUES(?,?,?,?,'DRAFT',?,?)", definitionID, code, name, description, scope, actor); err != nil {
		return Definition{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_versions(id,definition_id,version_number,graph_json,graph_sha256,status,created_by) VALUES(?,?,1,?,?,'DRAFT',?)", versionID, definitionID, raw, digest[:], actor); err != nil {
		return Definition{}, err
	}
	if err = tx.Commit(); err != nil {
		return Definition{}, err
	}
	return Definition{ID: hex.EncodeToString(definitionID), Code: code, Name: name, Description: description, Status: "DRAFT", Version: 1, DraftVersionID: hex.EncodeToString(versionID)}, nil
}
func (s *Service) CreateRevision(ctx context.Context, definitionHex, actorHex string) (string, error) {
	definitionID, err := decode(definitionHex)
	if err != nil {
		return "", err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var current []byte
	if err = tx.QueryRowContext(ctx, "SELECT current_version_id FROM workflow_definitions WHERE id=? FOR UPDATE", definitionID).Scan(&current); err != nil {
		return "", err
	}
	var raw, layout []byte
	if err = tx.QueryRowContext(ctx, "SELECT graph_json,COALESCE(layout_json,JSON_OBJECT()) FROM workflow_versions WHERE id=? AND status='PUBLISHED'", current).Scan(&raw, &layout); err != nil {
		return "", err
	}
	var revision uint64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_number),0)+1 FROM workflow_versions WHERE definition_id=?", definitionID).Scan(&revision); err != nil {
		return "", err
	}
	newVersion, _ := id()
	digest := sha256.Sum256(raw)
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_versions(id,definition_id,version_number,graph_json,layout_json,graph_sha256,status,created_by) VALUES(?,?,?,?,?,?,'DRAFT',?)", newVersion, definitionID, revision, raw, layout, digest[:], actor); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return hex.EncodeToString(newVersion), nil
}
func (s *Service) UpdateDraft(ctx context.Context, versionHex string, g Graph, layout any, expected uint64) error {
	versionID, err := decode(versionHex)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	layoutRaw, err := json.Marshal(layout)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	result, err := s.db.ExecContext(ctx, "UPDATE workflow_versions SET graph_json=?,layout_json=?,graph_sha256=?,status='DRAFT',validated_hash=NULL,validated_by=NULL,validated_at=NULL,version=version+1 WHERE id=? AND status IN ('DRAFT','VALIDATED') AND version=?", raw, layoutRaw, digest[:], versionID, expected)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("workflow draft version conflict")
	}
	return nil
}
func (s *Service) GetVersion(ctx context.Context, versionHex string) (VersionDetail, error) {
	versionID, err := decode(versionHex)
	if err != nil {
		return VersionDetail{}, err
	}
	var v VersionDetail
	if err = s.db.QueryRowContext(ctx, `SELECT HEX(id),HEX(definition_id),version_number,status,graph_json,COALESCE(layout_json,JSON_OBJECT()),version FROM workflow_versions WHERE id=?`, versionID).Scan(&v.ID, &v.DefinitionID, &v.VersionNumber, &v.Status, &v.Graph, &v.Layout, &v.Version); err != nil {
		return VersionDetail{}, err
	}
	v.ID = strings.ToLower(v.ID)
	v.DefinitionID = strings.ToLower(v.DefinitionID)
	return v, nil
}
func (s *Service) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(b.id),b.direction,b.scope_type,COALESCE(HEX(b.scope_id),''),HEX(b.workflow_version_id),COALESCE(d.code,''),d.name,v.version_number,b.enabled FROM workflow_bindings b JOIN workflow_versions v ON v.id=b.workflow_version_id JOIN workflow_definitions d ON d.id=v.definition_id ORDER BY b.direction,b.scope_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if err = rows.Scan(&b.ID, &b.Direction, &b.ScopeType, &b.ScopeID, &b.WorkflowVersionID, &b.WorkflowCode, &b.WorkflowName, &b.VersionNumber, &b.Enabled); err != nil {
			return nil, err
		}
		b.ID = strings.ToLower(b.ID)
		b.ScopeID = strings.ToLower(b.ScopeID)
		b.WorkflowVersionID = strings.ToLower(b.WorkflowVersionID)
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Service) List(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT HEX(d.id),COALESCE(d.code,''),d.name,COALESCE(d.description,''),d.status,d.version,COALESCE(HEX((SELECT v.id FROM workflow_versions v WHERE v.definition_id=d.id AND v.status IN ('DRAFT','VALIDATED') ORDER BY v.version_number DESC LIMIT 1)),''),COALESCE((SELECT v.status FROM workflow_versions v WHERE v.definition_id=d.id AND v.status IN ('DRAFT','VALIDATED') ORDER BY v.version_number DESC LIMIT 1),''),COALESCE(HEX(d.current_version_id),'') FROM workflow_definitions d ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		if err = rows.Scan(&d.ID, &d.Code, &d.Name, &d.Description, &d.Status, &d.Version, &d.DraftVersionID, &d.DraftStatus, &d.CurrentVersionID); err != nil {
			return nil, err
		}
		d.ID = strings.ToLower(d.ID)
		d.DraftVersionID = strings.ToLower(d.DraftVersionID)
		d.CurrentVersionID = strings.ToLower(d.CurrentVersionID)
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) ValidateVersion(ctx context.Context, versionHex, actorHex string) ([]ValidationError, error) {
	versionID, err := decode(versionHex)
	if err != nil {
		return nil, err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = s.db.QueryRowContext(ctx, "SELECT graph_json FROM workflow_versions WHERE id=? AND status IN ('DRAFT','VALIDATED')", versionID).Scan(&raw); err != nil {
		return nil, err
	}
	g, err := Parse(raw)
	if err != nil {
		return []ValidationError{{Code: "INVALID_JSON", Message: err.Error()}}, nil
	}
	validation := Validate(g)
	canonical, _ := json.Marshal(g)
	digest := sha256.Sum256(canonical)
	status := "PASSED"
	if len(validation) > 0 {
		status = "FAILED"
	}
	errorsJSON, _ := json.Marshal(validation)
	validationID, _ := id()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO workflow_validations(id,workflow_version_id,graph_hash,validator_version,status,errors_json,created_by) VALUES(?,?,?,'m5-validator-v1',?,?,?)", validationID, versionID, digest[:], status, errorsJSON, actor); err != nil {
		return nil, err
	}
	if status == "PASSED" {
		if _, err = tx.ExecContext(ctx, "UPDATE workflow_versions SET status='VALIDATED',validated_hash=?,validated_by=?,validated_at=UTC_TIMESTAMP(6) WHERE id=?", digest[:], actor, versionID); err != nil {
			return nil, err
		}
	}
	return validation, tx.Commit()
}
func (s *Service) Simulate(ctx context.Context, versionHex string, input map[string]any) ([]string, error) {
	versionID, err := decode(versionHex)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err = s.db.QueryRowContext(ctx, "SELECT graph_json FROM workflow_versions WHERE id=?", versionID).Scan(&raw); err != nil {
		return nil, err
	}
	g, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if validation := Validate(g); len(validation) > 0 {
		return nil, errors.New("workflow is invalid")
	}
	return SelectPath(g, input)
}
func (s *Service) Publish(ctx context.Context, versionHex, actorHex, changeNote string) error {
	versionID, err := decode(versionHex)
	if err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var graphHash, validatedHash []byte
	var definitionID []byte
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT definition_id,status,graph_sha256,validated_hash FROM workflow_versions WHERE id=? FOR UPDATE", versionID).Scan(&definitionID, &status, &graphHash, &validatedHash); err != nil {
		return err
	}
	if status != "VALIDATED" || !strings.EqualFold(hex.EncodeToString(graphHash), hex.EncodeToString(validatedHash)) {
		return errors.New("workflow must be validated at current hash")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_versions SET status='PUBLISHED',published_by=?,published_at=UTC_TIMESTAMP(6),change_note=? WHERE id=?", actor, changeNote, versionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workflow_definitions SET status='PUBLISHED',current_version_id=?,version=version+1 WHERE id=?", versionID, definitionID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Bind(ctx context.Context, direction, scopeType, scopeHex, versionHex, actorHex string) error {
	direction = strings.ToUpper(direction)
	scopeType = strings.ToUpper(scopeType)
	if direction != "OFFICE_TO_PROD" && direction != "PROD_TO_OFFICE" {
		return errors.New("invalid direction")
	}
	versionID, err := decode(versionHex)
	if err != nil {
		return err
	}
	actor, err := decode(actorHex)
	if err != nil {
		return err
	}
	var scope any
	if scopeType == "GLOBAL" {
		scope = nil
	} else if scopeType == "DEPARTMENT" || scopeType == "GROUP" || scopeType == "BUSINESS_SYSTEM" {
		scope, err = decode(scopeHex)
		if err != nil {
			return err
		}
	} else {
		return errors.New("invalid scope")
	}
	var published int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_versions WHERE id=? AND status='PUBLISHED'", versionID).Scan(&published); err != nil || published == 0 {
		return errors.New("workflow version is not published")
	}
	bindingID, _ := id()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if scope == nil {
		if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_bindings WHERE direction=? AND scope_type='GLOBAL'", direction); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workflow_bindings(id,direction,scope_type,scope_id,workflow_version_id,created_by) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE workflow_version_id=VALUES(workflow_version_id),enabled=TRUE,version=version+1`, bindingID, direction, scopeType, scope, versionID, actor); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) Resolve(ctx context.Context, direction, groupHex, businessHex string) (Selection, error) {
	groupID, err := decode(groupHex)
	if err != nil {
		return Selection{}, err
	}
	var departmentID []byte
	if err = s.db.QueryRowContext(ctx, "SELECT department_id FROM org_groups WHERE id=?", groupID).Scan(&departmentID); err != nil {
		return Selection{}, err
	}
	var business any
	if businessHex != "" {
		business, _ = decode(businessHex)
	}
	var versionID, raw, digest []byte
	err = s.db.QueryRowContext(ctx, `SELECT v.id,v.graph_json,v.graph_sha256 FROM workflow_bindings b JOIN workflow_versions v ON v.id=b.workflow_version_id WHERE b.direction=? AND b.enabled=TRUE AND v.status='PUBLISHED' AND ((b.scope_type='BUSINESS_SYSTEM' AND b.scope_id=?) OR (b.scope_type='GROUP' AND b.scope_id=?) OR (b.scope_type='DEPARTMENT' AND b.scope_id=?) OR b.scope_type='GLOBAL') ORDER BY FIELD(b.scope_type,'BUSINESS_SYSTEM','GROUP','DEPARTMENT','GLOBAL') LIMIT 1`, direction, business, groupID, departmentID).Scan(&versionID, &raw, &digest)
	if err != nil {
		return Selection{}, err
	}
	return Selection{VersionID: hex.EncodeToString(versionID), Graph: raw, Hash: hex.EncodeToString(digest)}, nil
}
func id() ([]byte, error) { b := make([]byte, 16); _, err := rand.Read(b); return b, err }
func decode(v string) ([]byte, error) {
	b, err := hex.DecodeString(v)
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid id")
	}
	return b, nil
}
